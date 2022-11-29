package utils

import (
	"io"
	"sync"
)

var (
	bufPool16k sync.Pool
	bufPool5k  sync.Pool
	bufPool2k  sync.Pool
	bufPool1k  sync.Pool
	bufPool    sync.Pool
)

// Join joins the two readers. Both are piped to each other and closed
// once everything has been read from each and written to the other.
func Join(local io.ReadWriter, remote io.ReadWriter) (bytesTransferred int64) {
	var wg sync.WaitGroup
	pipe := func(to io.ReadWriter, from io.ReadWriter, count *int64) {
		defer wg.Done()
		buf := GetBuf(16 * 1024)
		defer PutBuf(buf)
		*count, _ = io.CopyBuffer(to, from, buf)
	}

	var inCount, outCount int64
	wg.Add(2)
	go pipe(local, remote, &inCount)
	go pipe(remote, local, &outCount)
	wg.Wait()
	return inCount + outCount
}

func GetBuf(size int) []byte {
	var x interface{}
	if size >= 16*1024 {
		x = bufPool16k.Get()
	} else if size >= 5*1024 {
		x = bufPool5k.Get()
	} else if size >= 2*1024 {
		x = bufPool2k.Get()
	} else if size >= 1*1024 {
		x = bufPool1k.Get()
	} else {
		x = bufPool.Get()
	}
	if x == nil {
		return make([]byte, size)
	}
	buf := x.([]byte)
	if cap(buf) < size {
		return make([]byte, size)
	}
	return buf[:size]
}

func PutBuf(buf []byte) {
	size := cap(buf)
	if size >= 16*1024 {
		bufPool16k.Put(buf)
	} else if size >= 5*1024 {
		bufPool5k.Put(buf)
	} else if size >= 2*1024 {
		bufPool2k.Put(buf)
	} else if size >= 1*1024 {
		bufPool1k.Put(buf)
	} else {
		bufPool.Put(buf)
	}
}
