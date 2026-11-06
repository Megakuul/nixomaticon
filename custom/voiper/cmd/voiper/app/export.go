package app

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/megakuul/voiper/internal/diagnostics"
	"github.com/megakuul/voiper/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) ExportDiagnostics(includeDeviceNames bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	snapshot := a.phone.Snapshot()
	input := diagnostics.Input{Version: version.Version(), Audio: diagnostics.Audio{Backend: snapshot.Audio.Backend, EchoCancellation: snapshot.Audio.EchoCancellation, NoiseSuppression: snapshot.Audio.NoiseSuppression}, IncludeDeviceNames: includeDeviceNames}
	for _, account := range snapshot.Accounts {
		input.AccountStates = append(input.AccountStates, account.State)
	}
	for _, call := range snapshot.Calls {
		s := call.Stats
		input.Calls = append(input.Calls, diagnostics.Call{
			EncoderBitrate:            s.EncoderBitrate,
			ReceiverReportAvailable:   s.ReceiverReportAvailable,
			RemotePacketLossPercent:   s.RemotePacketLossPercent,
			ReceiverReportAgeSeconds:  s.ReceiverReportAgeSeconds,
			SendIdleSeconds:           s.SendIdleSeconds,
			ReceiveIdleSeconds:        s.ReceiveIdleSeconds,
			ICERestartPending:         s.ICERestartPending,
			State:                     call.State,
			Direction:                 call.Direction,
			Codec:                     s.Codec,
			Transport:                 s.Transport,
			ICEState:                  s.ICEState,
			ICECandidateType:          s.ICECandidateType,
			Encrypted:                 s.Encrypted,
			Muted:                     s.Muted,
			Held:                      s.Held,
			EarlyMedia:                s.EarlyMedia,
			AudioStopped:              s.AudioStopped,
			AudioRecovering:           s.AudioRecovering,
			Conference:                s.Conference,
			RTCPMux:                   s.RTCPMux,
			SampleRate:                s.SampleRate,
			ClockRate:                 s.ClockRate,
			PacketizationMilliseconds: s.PacketizationMilliseconds,
			BufferMilliseconds:        s.BufferMilliseconds,
			PacketsSent:               s.PacketsSent,
			PacketsReceived:           s.PacketsReceived,
			PacketsLost:               s.PacketsLost,
			PacketsDropped:            s.PacketsDropped,
			BytesSent:                 s.BytesSent,
			BytesReceived:             s.BytesReceived,
			CaptureOverruns:           s.CaptureOverruns,
			PlaybackUnderruns:         s.PlaybackUnderruns,
			AudioRecoveryAttempts:     s.AudioRecoveryAttempts,
			InputLevel:                s.InputLevel,
			OutputLevel:               s.OutputLevel,
			JitterMilliseconds:        s.JitterMilliseconds,
			RTTMilliseconds:           s.RTTMilliseconds,
		})
	}
	if includeDeviceNames {
		devices, err := a.AudioDevices()
		if err != nil {
			return err
		}
		for _, device := range devices {
			input.DeviceNames = append(input.DeviceNames, device.Name)
		}
	}
	raw, err := diagnostics.Export(input)
	if err != nil {
		return err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export anonymous diagnostics", DefaultFilename: "voiper-diagnostics-" + time.Now().Format("20060102-150405") + ".json", Filters: []runtime.FileFilter{{DisplayName: "JSON report", Pattern: "*.json"}}})
	if err != nil || path == "" {
		return err
	}
	return writeExport(path, raw)
}
func (a *App) ExportContacts(format string) error {
	if err := a.ready(); err != nil {
		return err
	}
	var text, extension string
	var err error
	switch format {
	case "csv":
		text, err = a.store.ExportCSV()
		extension = "csv"
	case "vcard":
		text, err = a.store.ExportVCard()
		extension = "vcf"
	default:
		return errors.New("choose CSV or vCard export")
	}
	if err != nil {
		return err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export contacts", DefaultFilename: "voiper-contacts." + extension})
	if err != nil || path == "" {
		return err
	}
	return writeExport(path, []byte(text))
}

func writeExport(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".voiper-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
