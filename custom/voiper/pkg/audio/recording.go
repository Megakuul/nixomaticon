package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Recording writes local and remote PCM as separate WAV channels. Producers only
// enqueue audio; disk writes and header finalization belong to the writer worker.
type Recording struct {
	Path              string
	capture, playback *Ring
	file              *os.File
	rate              int
	stop, done        chan struct{}
	once              sync.Once
	drops             atomic.Uint64
	mu                sync.Mutex
	err               error
}

func NewRecording(path string, rate int) (*Recording, error) {
	if rate < 8000 || rate > 48000 {
		return nil, errors.New("unsupported recording sample rate")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create recording: %w", err)
	}
	if _, err = file.Write(wavHeader(rate, 0)); err != nil {
		file.Close()
		return nil, err
	}
	r := &Recording{Path: path, file: file, rate: rate, capture: NewRing(rate), playback: NewRing(rate), stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r, nil
}
func (r *Recording) Capture(pcm []int16)  { r.enqueue(r.capture, pcm) }
func (r *Recording) Playback(pcm []int16) { r.enqueue(r.playback, pcm) }
func (r *Recording) enqueue(queue *Ring, pcm []int16) {
	data := make([]byte, len(pcm)*2)
	for i, sample := range pcm {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	if n := queue.Write(data); n < len(data) {
		r.drops.Add(1)
	}
}
func (r *Recording) Done() <-chan struct{} { return r.done }
func (r *Recording) Err() error            { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *Recording) Stop() error           { r.once.Do(func() { close(r.stop) }); <-r.done; return r.Err() }
func (r *Recording) run() {
	var total uint32
	var failure error
	defer func() {
		if _, err := r.file.WriteAt(wavHeader(r.rate, total), 0); err != nil {
			failure = errors.Join(failure, err)
		}
		if err := r.file.Close(); err != nil {
			failure = errors.Join(failure, err)
		}
		r.mu.Lock()
		r.err = failure
		r.mu.Unlock()
		close(r.done)
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	local, remote := make([]byte, r.rate/50*2), make([]byte, r.rate/50*2)
	stereo := make([]byte, len(local)*2)
	stopping := false
	for {
		if !stopping {
			select {
			case <-r.stop:
				stopping = true
			case <-ticker.C:
			}
		}
		if r.drops.Load() > 0 {
			failure = errors.New("recording stopped: disk writer could not keep up")
			return
		}
		a, b := r.capture.Read(local), r.playback.Read(remote)
		if stopping && a == 0 && b == 0 {
			return
		}
		clear(local[a:])
		clear(remote[b:])
		size := len(local)
		if stopping {
			size = max(a, b)
		}
		for i := 0; i < size; i += 2 {
			copy(stereo[i*2:i*2+2], local[i:i+2])
			copy(stereo[i*2+2:i*2+4], remote[i:i+2])
		}
		chunk := stereo[:size*2]
		if uint64(total)+uint64(len(chunk)) > 0xffffffff-36 {
			failure = errors.New("recording reached the WAV file size limit")
			return
		}
		n, err := r.file.Write(chunk)
		total += uint32(n)
		if err != nil {
			failure = fmt.Errorf("write recording: %w", err)
			return
		}
	}
}
func wavHeader(rate int, size uint32) []byte {
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], size+36)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 2)
	binary.LittleEndian.PutUint32(header[24:], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(rate*4))
	binary.LittleEndian.PutUint16(header[32:], 4)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], size)
	return header
}
