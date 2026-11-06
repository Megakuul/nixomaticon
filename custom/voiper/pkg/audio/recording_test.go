package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordingKeepsBothChannelsAndFinalizesHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "call.wav")
	recording, err := NewRecording(path, 8000)
	if err != nil {
		t.Fatal(err)
	}
	recording.Capture([]int16{100, 200})
	recording.Playback([]int16{-100, -200})
	if err = recording.Stop(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 52 || string(data[:4]) != "RIFF" || binary.LittleEndian.Uint32(data[40:]) != 8 {
		t.Fatalf("invalid WAV header or length: %d", len(data))
	}
	for i, want := range []int16{100, -100, 200, -200} {
		if got := int16(binary.LittleEndian.Uint16(data[44+i*2:])); got != want {
			t.Fatalf("sample %d: got %d want %d", i, got, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	if _, err = NewRecording(path, 8000); err == nil {
		t.Fatal("recording overwrote an existing file")
	}
}
