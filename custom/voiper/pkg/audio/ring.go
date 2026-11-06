package audio

import "sync/atomic"

// Ring is a bounded single-producer, single-consumer PCM byte queue. Device
// callbacks never wait for a consumer; full queues drop new input.
type Ring struct {
	data    []byte
	read    atomic.Uint64
	written atomic.Uint64
}

func NewRing(size int) *Ring {
	if size < 1 {
		panic("audio ring size must be positive")
	}
	return &Ring{data: make([]byte, size)}
}
func (r *Ring) Write(src []byte) int {
	w := r.written.Load()
	available := uint64(len(r.data)) - (w - r.read.Load())
	n := min(len(src), int(available))
	for i := 0; i < n; i++ {
		r.data[(w+uint64(i))%uint64(len(r.data))] = src[i]
	}
	r.written.Store(w + uint64(n))
	return n
}
func (r *Ring) Read(dst []byte) int {
	pos := r.read.Load()
	n := min(len(dst), int(r.written.Load()-pos))
	for i := 0; i < n; i++ {
		dst[i] = r.data[(pos+uint64(i))%uint64(len(r.data))]
	}
	r.read.Store(pos + uint64(n))
	return n
}

// Mix sums mono streams using a wide accumulator before saturating. Excluding a
// participant provides mix-minus so that participant does not hear their own voice.
func Mix(dst []int16, streams [][]int16, exclude int) {
	for i := range dst {
		sum := int64(0)
		for j, stream := range streams {
			if j != exclude && i < len(stream) {
				sum += int64(stream[i])
			}
		}
		dst[i] = int16(max(-32768, min(32767, sum)))
	}
}

// Discard is called only by the consumer when starting or resynchronizing a stream.
func (r *Ring) Discard() { r.read.Store(r.written.Load()) }
