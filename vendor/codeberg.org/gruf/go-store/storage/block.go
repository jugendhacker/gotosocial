package storage

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"

	"codeberg.org/gruf/go-byteutil"
	"codeberg.org/gruf/go-errors/v2"
	"codeberg.org/gruf/go-fastcopy"
	"codeberg.org/gruf/go-hashenc"
	"codeberg.org/gruf/go-pools"
	"codeberg.org/gruf/go-store/util"
)

var (
	nodePathPrefix  = "node/"
	blockPathPrefix = "block/"
)

// DefaultBlockConfig is the default BlockStorage configuration
var DefaultBlockConfig = &BlockConfig{
	BlockSize:    1024 * 16,
	WriteBufSize: 4096,
	Overwrite:    false,
	Compression:  NoCompression(),
}

// BlockConfig defines options to be used when opening a BlockStorage
type BlockConfig struct {
	// BlockSize is the chunking size to use when splitting and storing blocks of data
	BlockSize int

	// ReadBufSize is the buffer size to use when reading node files
	ReadBufSize int

	// WriteBufSize is the buffer size to use when writing file streams (PutStream)
	WriteBufSize int

	// Overwrite allows overwriting values of stored keys in the storage
	Overwrite bool

	// Compression is the Compressor to use when reading / writing files, default is no compression
	Compression Compressor
}

// getBlockConfig returns a valid BlockConfig for supplied ptr
func getBlockConfig(cfg *BlockConfig) BlockConfig {
	// If nil, use default
	if cfg == nil {
		cfg = DefaultBlockConfig
	}

	// Assume nil compress == none
	if cfg.Compression == nil {
		cfg.Compression = NoCompression()
	}

	// Assume 0 chunk size == use default
	if cfg.BlockSize < 1 {
		cfg.BlockSize = DefaultBlockConfig.BlockSize
	}

	// Assume 0 buf size == use default
	if cfg.WriteBufSize < 1 {
		cfg.WriteBufSize = DefaultDiskConfig.WriteBufSize
	}

	// Return owned config copy
	return BlockConfig{
		BlockSize:    cfg.BlockSize,
		WriteBufSize: cfg.WriteBufSize,
		Overwrite:    cfg.Overwrite,
		Compression:  cfg.Compression,
	}
}

// BlockStorage is a Storage implementation that stores input data as chunks on
// a filesystem. Each value is chunked into blocks of configured size and these
// blocks are stored with name equal to their base64-encoded SHA256 hash-sum. A
// "node" file is finally created containing an array of hashes contained within
// this value
type BlockStorage struct {
	path      string            // path is the root path of this store
	blockPath string            // blockPath is the joined root path + block path prefix
	nodePath  string            // nodePath is the joined root path + node path prefix
	config    BlockConfig       // cfg is the supplied configuration for this store
	hashPool  sync.Pool         // hashPool is this store's hashEncoder pool
	bufpool   pools.BufferPool  // bufpool is this store's bytes.Buffer pool
	cppool    fastcopy.CopyPool // cppool is the prepared io copier with buffer pool
	lock      *Lock             // lock is the opened lockfile for this storage instance

	// NOTE:
	// BlockStorage does not need to lock each of the underlying block files
	// as the filename itself directly relates to the contents. If there happens
	// to be an overwrite, it will just be of the same data since the filename is
	// the hash of the data.
}

// OpenBlock opens a BlockStorage instance for given folder path and configuration
func OpenBlock(path string, cfg *BlockConfig) (*BlockStorage, error) {
	// Acquire path builder
	pb := util.GetPathBuilder()
	defer util.PutPathBuilder(pb)

	// Clean provided path, ensure ends in '/' (should
	// be dir, this helps with file path trimming later)
	path = pb.Clean(path) + "/"

	// Get checked config
	config := getBlockConfig(cfg)

	// Attempt to open path
	file, err := os.OpenFile(path, defaultFileROFlags, defaultDirPerms)
	if err != nil {
		// If not a not-exist error, return
		if !os.IsNotExist(err) {
			return nil, err
		}

		// Attempt to make store path dirs
		err = os.MkdirAll(path, defaultDirPerms)
		if err != nil {
			return nil, err
		}

		// Reopen dir now it's been created
		file, err = os.OpenFile(path, defaultFileROFlags, defaultDirPerms)
		if err != nil {
			return nil, err
		}
	}
	defer file.Close()

	// Double check this is a dir (NOT a file!)
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	} else if !stat.IsDir() {
		return nil, errPathIsFile
	}

	// Open and acquire storage lock for path
	lock, err := OpenLock(pb.Join(path, LockFile))
	if err != nil {
		return nil, err
	}

	// Figure out the largest size for bufpool slices
	bufSz := encodedHashLen
	if bufSz < config.BlockSize {
		bufSz = config.BlockSize
	}
	if bufSz < config.WriteBufSize {
		bufSz = config.WriteBufSize
	}

	// Prepare BlockStorage
	st := &BlockStorage{
		path:      path,
		blockPath: pb.Join(path, blockPathPrefix),
		nodePath:  pb.Join(path, nodePathPrefix),
		config:    config,
		hashPool: sync.Pool{
			New: func() interface{} {
				return newHashEncoder()
			},
		},
		bufpool: pools.NewBufferPool(bufSz),
		lock:    lock,
	}

	// Set copypool buffer size
	st.cppool.Buffer(config.ReadBufSize)

	return st, nil
}

// Clean implements storage.Clean()
func (st *BlockStorage) Clean() error {
	// Track open
	st.lock.Add()
	defer st.lock.Done()

	// Check if open
	if st.lock.Closed() {
		return ErrClosed
	}

	// Acquire path builder
	pb := util.GetPathBuilder()
	defer util.PutPathBuilder(pb)

	nodes := map[string]*node{}
	onceErr := errors.OnceError{}

	// Walk nodes dir for entries
	err := util.WalkDir(pb, st.nodePath, func(npath string, fsentry fs.DirEntry) {
		// Only deal with regular files
		if !fsentry.Type().IsRegular() {
			return
		}

		// Stop if we hit error previously
		if onceErr.IsSet() {
			return
		}

		// Get joined node path name
		npath = pb.Join(npath, fsentry.Name())

		// Attempt to open RO file
		file, err := open(npath, defaultFileROFlags)
		if err != nil {
			onceErr.Store(err)
			return
		}
		defer file.Close()

		// Alloc new Node + acquire hash buffer for writes
		hbuf := st.bufpool.Get()
		defer st.bufpool.Put(hbuf)
		hbuf.Guarantee(encodedHashLen)
		node := node{}

		// Write file contents to node
		_, err = io.CopyBuffer(
			&nodeWriter{
				node: &node,
				buf:  hbuf,
			},
			file,
			nil,
		)
		if err != nil {
			onceErr.Store(err)
			return
		}

		// Append to nodes slice
		nodes[fsentry.Name()] = &node
	})

	// Handle errors (though nodePath may not have been created yet)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if onceErr.IsSet() {
		return onceErr.Load()
	}

	// Walk blocks dir for entries
	onceErr.Reset()
	err = util.WalkDir(pb, st.blockPath, func(bpath string, fsentry fs.DirEntry) {
		// Only deal with regular files
		if !fsentry.Type().IsRegular() {
			return
		}

		// Stop if we hit error previously
		if onceErr.IsSet() {
			return
		}

		inUse := false
		for key, node := range nodes {
			if node.removeHash(fsentry.Name()) {
				if len(node.hashes) < 1 {
					// This node contained hash, and after removal is now empty.
					// Remove this node from our tracked nodes slice
					delete(nodes, key)
				}
				inUse = true
			}
		}

		// Block hash is used by node
		if inUse {
			return
		}

		// Get joined block path name
		bpath = pb.Join(bpath, fsentry.Name())

		// Remove this unused block path
		err := os.Remove(bpath)
		if err != nil {
			onceErr.Store(err)
			return
		}
	})

	// Handle errors (though blockPath may not have been created yet)
	if err != nil && !os.IsNotExist(err) {
		return err
	} else if onceErr.IsSet() {
		return onceErr.Load()
	}

	// If there are nodes left at this point, they are corrupt
	// (i.e. they're referencing block hashes that don't exist)
	if len(nodes) > 0 {
		nodeKeys := []string{}
		for key := range nodes {
			nodeKeys = append(nodeKeys, key)
		}
		return fmt.Errorf("store/storage: corrupted nodes: %v", nodeKeys)
	}

	return nil
}

// ReadBytes implements Storage.ReadBytes()
func (st *BlockStorage) ReadBytes(key string) ([]byte, error) {
	// Get stream reader for key
	rc, err := st.ReadStream(key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// Read all bytes and return
	return io.ReadAll(rc)
}

// ReadStream implements Storage.ReadStream()
func (st *BlockStorage) ReadStream(key string) (io.ReadCloser, error) {
	// Get node file path for key
	npath, err := st.nodePathForKey(key)
	if err != nil {
		return nil, err
	}

	// Track open
	st.lock.Add()

	// Check if open
	if st.lock.Closed() {
		st.lock.Done()
		return nil, ErrClosed
	}

	// Attempt to open RO file
	file, err := open(npath, defaultFileROFlags)
	if err != nil {
		st.lock.Done()
		return nil, errSwapNotFound(err)
	}
	defer file.Close()

	// Acquire hash buffer for writes
	hbuf := st.bufpool.Get()
	defer st.bufpool.Put(hbuf)

	// Write file contents to node
	node := node{}
	_, err = st.cppool.Copy(
		&nodeWriter{
			node: &node,
			buf:  hbuf,
		},
		file,
	)
	if err != nil {
		st.lock.Done()
		return nil, err
	}

	// Prepare block reader and return
	rc := util.NopReadCloser(&blockReader{
		storage: st,
		node:    &node,
	}) // we wrap the blockreader to decr lockfile waitgroup
	return util.ReadCloserWithCallback(rc, st.lock.Done), nil
}

func (st *BlockStorage) readBlock(key string) ([]byte, error) {
	// Get block file path for key
	bpath := st.blockPathForKey(key)

	// Attempt to open RO file
	file, err := open(bpath, defaultFileROFlags)
	if err != nil {
		return nil, wrap(errCorruptNode, err)
	}
	defer file.Close()

	// Wrap the file in a compressor
	cFile, err := st.config.Compression.Reader(file)
	if err != nil {
		return nil, wrap(errCorruptNode, err)
	}
	defer cFile.Close()

	// Read the entire file
	return io.ReadAll(cFile)
}

// WriteBytes implements Storage.WriteBytes()
func (st *BlockStorage) WriteBytes(key string, value []byte) error {
	return st.WriteStream(key, bytes.NewReader(value))
}

// WriteStream implements Storage.WriteStream()
func (st *BlockStorage) WriteStream(key string, r io.Reader) error {
	// Get node file path for key
	npath, err := st.nodePathForKey(key)
	if err != nil {
		return err
	}

	// Track open
	st.lock.Add()
	defer st.lock.Done()

	// Check if open
	if st.lock.Closed() {
		return ErrClosed
	}

	// Check if this exists
	ok, err := stat(key)
	if err != nil {
		return err
	}

	// Check if we allow overwrites
	if ok && !st.config.Overwrite {
		return ErrAlreadyExists
	}

	// Ensure nodes dir (and any leading up to) exists
	err = os.MkdirAll(st.nodePath, defaultDirPerms)
	if err != nil {
		return err
	}

	// Ensure blocks dir (and any leading up to) exists
	err = os.MkdirAll(st.blockPath, defaultDirPerms)
	if err != nil {
		return err
	}

	// Alloc new node
	node := node{}

	// Acquire HashEncoder
	hc := st.hashPool.Get().(*hashEncoder)
	defer st.hashPool.Put(hc)

	// Create new waitgroup and OnceError for
	// goroutine error tracking and propagating
	wg := sync.WaitGroup{}
	onceErr := errors.OnceError{}

loop:
	for !onceErr.IsSet() {
		// Fetch new buffer for this loop
		buf := st.bufpool.Get()
		buf.Grow(st.config.BlockSize)

		// Read next chunk
		n, err := io.ReadFull(r, buf.B)
		switch err {
		case nil, io.ErrUnexpectedEOF:
			// do nothing
		case io.EOF:
			st.bufpool.Put(buf)
			break loop
		default:
			st.bufpool.Put(buf)
			return err
		}

		// Hash the encoded data
		sum := hc.EncodeSum(buf.B)

		// Append to the node's hashes
		node.hashes = append(node.hashes, sum)

		// If already on disk, skip
		has, err := st.statBlock(sum)
		if err != nil {
			st.bufpool.Put(buf)
			return err
		} else if has {
			st.bufpool.Put(buf)
			continue loop
		}

		// Check if reached EOF
		atEOF := (n < buf.Len())

		wg.Add(1)
		go func() {
			// Perform writes in goroutine

			defer func() {
				// Defer release +
				// signal we're done
				st.bufpool.Put(buf)
				wg.Done()
			}()

			// Write block to store at hash
			err = st.writeBlock(sum, buf.B[:n])
			if err != nil {
				onceErr.Store(err)
				return
			}
		}()

		// Break at end
		if atEOF {
			break loop
		}
	}

	// Wait, check errors
	wg.Wait()
	if onceErr.IsSet() {
		return onceErr.Load()
	}

	// If no hashes created, return
	if len(node.hashes) < 1 {
		return errNoHashesWritten
	}

	// Prepare to swap error if need-be
	errSwap := errSwapNoop

	// Build file RW flags
	// NOTE: we performed an initial check for
	//       this before writing blocks, but if
	//       the utilizer of this storage didn't
	//       correctly mutex protect this key then
	//       someone may have beaten us to the
	//       punch at writing the node file.
	flags := defaultFileRWFlags
	if !st.config.Overwrite {
		flags |= syscall.O_EXCL

		// Catch + replace err exist
		errSwap = errSwapExist
	}

	// Attempt to open RW file
	file, err := open(npath, flags)
	if err != nil {
		return errSwap(err)
	}
	defer file.Close()

	// Acquire write buffer
	buf := st.bufpool.Get()
	defer st.bufpool.Put(buf)
	buf.Grow(st.config.WriteBufSize)

	// Finally, write data to file
	_, err = io.CopyBuffer(file, &nodeReader{node: &node}, nil)
	return err
}

// writeBlock writes the block with hash and supplied value to the filesystem
func (st *BlockStorage) writeBlock(hash string, value []byte) error {
	// Get block file path for key
	bpath := st.blockPathForKey(hash)

	// Attempt to open RW file
	file, err := open(bpath, defaultFileRWFlags)
	if err != nil {
		if err == syscall.EEXIST {
			err = nil /* race issue describe in struct NOTE */
		}
		return err
	}
	defer file.Close()

	// Wrap the file in a compressor
	cFile, err := st.config.Compression.Writer(file)
	if err != nil {
		return err
	}
	defer cFile.Close()

	// Write value to file
	_, err = cFile.Write(value)
	return err
}

// statBlock checks for existence of supplied block hash
func (st *BlockStorage) statBlock(hash string) (bool, error) {
	return stat(st.blockPathForKey(hash))
}

// Stat implements Storage.Stat()
func (st *BlockStorage) Stat(key string) (bool, error) {
	// Get node file path for key
	kpath, err := st.nodePathForKey(key)
	if err != nil {
		return false, err
	}

	// Track open
	st.lock.Add()
	defer st.lock.Done()

	// Check if open
	if st.lock.Closed() {
		return false, ErrClosed
	}

	// Check for file on disk
	return stat(kpath)
}

// Remove implements Storage.Remove()
func (st *BlockStorage) Remove(key string) error {
	// Get node file path for key
	kpath, err := st.nodePathForKey(key)
	if err != nil {
		return err
	}

	// Track open
	st.lock.Add()
	defer st.lock.Done()

	// Check if open
	if st.lock.Closed() {
		return ErrClosed
	}

	// Remove at path (we know this is file)
	if err := unlink(kpath); err != nil {
		return errSwapNotFound(err)
	}

	return nil
}

// Close implements Storage.Close()
func (st *BlockStorage) Close() error {
	return st.lock.Close()
}

// WalkKeys implements Storage.WalkKeys()
func (st *BlockStorage) WalkKeys(opts WalkKeysOptions) error {
	// Track open
	st.lock.Add()
	defer st.lock.Done()

	// Check if open
	if st.lock.Closed() {
		return ErrClosed
	}

	// Acquire path builder
	pb := util.GetPathBuilder()
	defer util.PutPathBuilder(pb)

	// Walk dir for entries
	return util.WalkDir(pb, st.nodePath, func(npath string, fsentry fs.DirEntry) {
		// Only deal with regular files
		if fsentry.Type().IsRegular() {
			opts.WalkFn(entry(fsentry.Name()))
		}
	})
}

// nodePathForKey calculates the node file path for supplied key
func (st *BlockStorage) nodePathForKey(key string) (string, error) {
	// Path separators are illegal, as directory paths
	if strings.Contains(key, "/") || key == "." || key == ".." {
		return "", ErrInvalidKey
	}

	// Acquire path builder
	pb := util.GetPathBuilder()
	defer util.PutPathBuilder(pb)

	// Append the nodepath to key
	pb.AppendString(st.nodePath)
	pb.AppendString(key)

	// Return joined + cleaned node-path
	return pb.Join(st.nodePath, key), nil
}

// blockPathForKey calculates the block file path for supplied hash
func (st *BlockStorage) blockPathForKey(hash string) string {
	pb := util.GetPathBuilder()
	defer util.PutPathBuilder(pb)
	return pb.Join(st.blockPath, hash)
}

// hashSeparator is the separating byte between block hashes
const hashSeparator = byte('\n')

// node represents the contents of a node file in storage
type node struct {
	hashes []string
}

// removeHash attempts to remove supplied block hash from the node's hash array
func (n *node) removeHash(hash string) bool {
	haveDropped := false
	for i := 0; i < len(n.hashes); {
		if n.hashes[i] == hash {
			// Drop this hash from slice
			n.hashes = append(n.hashes[:i], n.hashes[i+1:]...)
			haveDropped = true
		} else {
			// Continue iter
			i++
		}
	}
	return haveDropped
}

// nodeReader is an io.Reader implementation for the node file representation,
// which is useful when calculated node file is being written to the store
type nodeReader struct {
	node *node
	idx  int
	last int
}

func (r *nodeReader) Read(b []byte) (int, error) {
	n := 0

	// '-1' means we missed writing
	// hash separator on last iteration
	if r.last == -1 {
		b[n] = hashSeparator
		n++
		r.last = 0
	}

	for r.idx < len(r.node.hashes) {
		hash := r.node.hashes[r.idx]

		// Copy into buffer + update read count
		m := copy(b[n:], hash[r.last:])
		n += m

		// If incomplete copy, return here
		if m < len(hash)-r.last {
			r.last = m
			return n, nil
		}

		// Check we can write last separator
		if n == len(b) {
			r.last = -1
			return n, nil
		}

		// Write separator, iter, reset
		b[n] = hashSeparator
		n++
		r.idx++
		r.last = 0
	}

	// We reached end of hashes
	return n, io.EOF
}

// nodeWriter is an io.Writer implementation for the node file representation,
// which is useful when calculated node file is being read from the store
type nodeWriter struct {
	node *node
	buf  *byteutil.Buffer
}

func (w *nodeWriter) Write(b []byte) (int, error) {
	n := 0

	for {
		// Find next hash separator position
		idx := bytes.IndexByte(b[n:], hashSeparator)
		if idx == -1 {
			// Check we shouldn't be expecting it
			if w.buf.Len() > encodedHashLen {
				return n, errInvalidNode
			}

			// Write all contents to buffer
			w.buf.Write(b[n:])
			return len(b), nil
		}

		// Found hash separator, write
		// current buf contents to Node hashes
		w.buf.Write(b[n : n+idx])
		n += idx + 1
		if w.buf.Len() != encodedHashLen {
			return n, errInvalidNode
		}

		// Append to hashes & reset
		w.node.hashes = append(w.node.hashes, w.buf.String())
		w.buf.Reset()
	}
}

// blockReader is an io.Reader implementation for the combined, linked block
// data contained with a node file. Basically, this allows reading value data
// from the store for a given node file
type blockReader struct {
	storage *BlockStorage
	node    *node
	buf     []byte
	prev    int
}

func (r *blockReader) Read(b []byte) (int, error) {
	n := 0

	// Data left in buf, copy as much as we
	// can into supplied read buffer
	if r.prev < len(r.buf)-1 {
		n += copy(b, r.buf[r.prev:])
		r.prev += n
		if n >= len(b) {
			return n, nil
		}
	}

	for {
		// Check we have any hashes left
		if len(r.node.hashes) < 1 {
			return n, io.EOF
		}

		// Get next key from slice
		key := r.node.hashes[0]
		r.node.hashes = r.node.hashes[1:]

		// Attempt to fetch next batch of data
		var err error
		r.buf, err = r.storage.readBlock(key)
		if err != nil {
			return n, err
		}
		r.prev = 0

		// Copy as much as can from new buffer
		m := copy(b[n:], r.buf)
		r.prev += m
		n += m

		// If we hit end of supplied buf, return
		if n >= len(b) {
			return n, nil
		}
	}
}

var (
	// base64Encoding is our base64 encoding object.
	base64Encoding = hashenc.Base64()

	// encodedHashLen is the once-calculated encoded hash-sum length
	encodedHashLen = base64Encoding.EncodedLen(
		sha256.New().Size(),
	)
)

// hashEncoder is a HashEncoder with built-in encode buffer
type hashEncoder struct {
	henc hashenc.HashEncoder
	ebuf []byte
}

// newHashEncoder returns a new hashEncoder instance
func newHashEncoder() *hashEncoder {
	return &hashEncoder{
		henc: hashenc.New(sha256.New(), base64Encoding),
		ebuf: make([]byte, encodedHashLen),
	}
}

// EncodeSum encodes the src data and returns resulting bytes, only valid until next call to EncodeSum()
func (henc *hashEncoder) EncodeSum(src []byte) string {
	henc.henc.EncodeSum(henc.ebuf, src)
	return string(henc.ebuf)
}
