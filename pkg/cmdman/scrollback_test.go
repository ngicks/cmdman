package cmdman

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestRingBufferBasic(t *testing.T) {
	r := newRingBuffer(10)

	r.Write([]byte("hello"))
	assert.Equal(t, string(r.Bytes()), "hello")

	r.Write([]byte("world"))
	assert.Equal(t, string(r.Bytes()), "helloworld")
}

func TestRingBufferWrap(t *testing.T) {
	r := newRingBuffer(10)

	r.Write([]byte("0123456789"))
	assert.Equal(t, string(r.Bytes()), "0123456789")

	r.Write([]byte("AB"))
	assert.Equal(t, string(r.Bytes()), "23456789AB")
}

func TestRingBufferOverflow(t *testing.T) {
	r := newRingBuffer(5)

	r.Write([]byte("1234567890"))
	assert.Equal(t, string(r.Bytes()), "67890")
}

func TestRingBufferEmpty(t *testing.T) {
	r := newRingBuffer(10)
	assert.Equal(t, len(r.Bytes()), 0)
}
