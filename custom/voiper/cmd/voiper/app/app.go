package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/megakuul/voiper/internal/config"
	"github.com/megakuul/voiper/internal/desktop"
	"github.com/megakuul/voiper/internal/directory"
	"github.com/megakuul/voiper/internal/phone"
	"github.com/megakuul/voiper/internal/store"
	"github.com/megakuul/voiper/internal/util"
	"github.com/megakuul/voiper/pkg/audio"
	"github.com/megakuul/voiper/pkg/media"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	initialized       chan struct{}
	callCommand       chan struct{}
	preferences       Preferences
	desktop           *desktop.Service
	cancel            context.CancelFunc
	workers           sync.WaitGroup
	closing, quitting bool
	desktopEvents     chan appEvent
	dialTarget        string
	ctx               context.Context
	basePath          string
	mu                sync.Mutex
	phone             *phone.Manager
	store             *store.Store
	directory         *directory.Service
	startupError      error
}
type AppOption func(*App)

func NewApp(opts ...AppOption) *App {
	a := &App{initialized: make(chan struct{}), callCommand: make(chan struct{}, 1), preferences: Preferences{Notifications: true}, desktopEvents: make(chan appEvent, 64)}
	for _, o := range opts {
		o(a)
	}
	return a
}
func WithBase(path string) AppOption { return func(a *App) { a.basePath = path } }
func (a *App) startup(ctx context.Context) {
	defer close(a.initialized)
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.ctx, a.cancel = ctx, cancel
	if a.closing {
		a.mu.Unlock()
		cancel()
		return
	}
	a.mu.Unlock()
	slog.SetDefault(slog.New(slog.NewJSONHandler(util.NewEventWriter(ctx, "log"), &slog.HandlerOptions{Level: slog.LevelInfo})))
	configRoot, err := os.UserConfigDir()
	if err != nil {
		a.startupError = err
		return
	}
	if a.basePath == "" {
		a.basePath = filepath.Join(configRoot, "voiper", "accounts")
	}
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			a.startupError = err
			return
		}
		dataRoot = filepath.Join(home, ".local", "share")
	}
	a.store, err = store.Open(filepath.Join(dataRoot, "voiper", "voiper.db"), filepath.Join(configRoot, "voiper", "contacts.toml"))
	if err != nil {
		a.startupError = err
		return
	}
	a.phone = phone.New(ctx, a.store, a.emitPhoneEvent)
	a.directory, err = directory.New(ctx, a.store, a.directoryPassword, func() { runtime.EventsEmit(ctx, "directory-changed", nil) })
	if err != nil {
		a.startupError = err
		return
	}
	preferences := Preferences{Notifications: true}
	if raw, err := os.ReadFile(filepath.Join(configRoot, "voiper", "preferences.json")); err == nil {
		var saved Preferences
		if json.Unmarshal(raw, &saved) == nil {
			preferences = saved
		} else {
			slog.Warn("Could not load preferences")
		}
	}
	if err := a.store.PruneMessages(preferences.MessageRetentionDays); err != nil {
		slog.Warn("Could not apply message retention", "error", err)
	}
	var settings media.Settings
	if raw, err := os.ReadFile(filepath.Join(configRoot, "voiper", "audio.json")); err == nil && json.Unmarshal(raw, &settings) == nil {
		a.phone.SetAudio(settings)
	}
	a.mu.Lock()
	a.preferences = preferences
	if !a.closing {
		a.workers.Add(1)
		go a.runDesktop()
	}
	a.mu.Unlock()
}
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	a.closing = true
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-a.initialized
	a.workers.Wait()
	if a.directory != nil {
		a.directory.Close()
	}
	if a.phone != nil {
		a.phone.Close()
	}
	if a.store != nil {
		a.store.Close()
	}
}
func (a *App) ready() error {
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		return errors.New("application is closing")
	}
	select {
	case <-a.initialized:
	default:
		return errors.New("application is starting")
	}
	if a.startupError != nil {
		return a.startupError
	}
	if a.phone == nil {
		return errors.New("application did not start")
	}
	return nil
}
func (a *App) ListConfigs() (map[string]bool, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return config.ListConfigs(a.basePath)
}
func (a *App) GetConfig(name, key string) (*config.Config, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	path, err := config.Path(a.basePath, name)
	if err != nil {
		return nil, err
	}
	return config.LoadConfig(path, key)
}
func (a *App) SetConfig(cfg *config.Config, name, key string) error {
	if err := a.ready(); err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("configuration is required")
	}
	if err := config.Validate(*cfg); err != nil {
		return err
	}
	path, err := config.Path(a.basePath, name)
	if err != nil {
		return err
	}
	opposite := path + config.CONFIG_EXTENSION
	if key == "" {
		opposite += config.SECURE_EXTENSION
	}
	if _, err := os.Lstat(opposite); err == nil {
		return errors.New("an account with this name uses the other encryption mode; use a different name")
	} else if !os.IsNotExist(err) {
		return err
	}
	stored := *cfg
	if stored.UseSecretService {
		service, err := a.secretService()
		if err != nil {
			return err
		}
		if stored.Password != "" {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			err = service.SetSecret(ctx, secretAccount(path), stored.Password)
			cancel()
			if err != nil {
				return err
			}
		}
		stored.Password = ""
		if stored.SwyxPassword != "" {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			err = service.SetSecret(ctx, swyxSecretAccount(path), stored.SwyxPassword)
			cancel()
			if err != nil {
				return err
			}
		}
		stored.SwyxPassword = ""
	}
	return config.WriteConfig(&stored, path, key)
}
func (a *App) RemoveConfig(name string, encrypted bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	path, err := config.Path(a.basePath, name)
	if err != nil {
		return err
	}
	a.phone.Disable(name)
	a.mu.Lock()
	defer a.mu.Unlock()
	return config.RemoveConfig(path, encrypted)
}
func (a *App) EnableConfig(name, key string) error {
	cfg, err := a.GetConfig(name, key)
	if err != nil {
		return err
	}
	if cfg.UseSecretService {
		service, err := a.secretService()
		if err != nil {
			return err
		}
		path, err := config.Path(a.basePath, name)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		password, err := service.GetSecret(ctx, secretAccount(path))
		cancel()
		if err != nil {
			return err
		}
		cfg.Password = password
		ctx, cancel = context.WithTimeout(a.ctx, 15*time.Second)
		swyxPassword, err := service.GetSecret(ctx, swyxSecretAccount(path))
		cancel()
		if err != nil && !errors.Is(err, desktop.ErrSecretNotFound) {
			return err
		}
		cfg.SwyxPassword = swyxPassword
	}
	return a.phone.Enable(name, *cfg)
}
func (a *App) RetryRegistration(name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.RetryRegistration(name)
}
func (a *App) DisableConfig(name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	a.phone.Disable(name)
	return nil
}
func (a *App) Snapshot() (phone.Snapshot, error) {
	if err := a.ready(); err != nil {
		return phone.Snapshot{}, err
	}
	snapshot := a.phone.Snapshot()
	unread, err := a.store.UnreadCount()
	snapshot.UnreadMessages = unread
	return snapshot, err
}
func (a *App) Dial(account, target string) error {
	return a.startCall(func() error { return a.phone.Dial(account, target) })
}
func (a *App) Answer(id string) error {
	return a.startCall(func() error { return a.phone.Answer(id) })
}
func (a *App) Hangup(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Hangup(id)
}
func (a *App) Mute(id string, v bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Mute(id, v)
}
func (a *App) Hold(id string, v bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Hold(id, v)
}
func (a *App) SendDTMF(id, digit string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.DTMF(id, digit)
}
func (a *App) Transfer(id, target string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Transfer(id, target)
}
func (a *App) SetDND(v bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	a.phone.SetDND(v)
	return nil
}
func (a *App) AudioDevices() ([]media.Device, error) { return media.Devices() }
func (a *App) SetAudio(settings media.Settings) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.phone.SetAudio(settings); err != nil {
		return err
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "voiper")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".audio-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "audio.json"))
}
func (a *App) Contacts(query string) ([]store.Contact, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.Contacts(query)
}
func (a *App) SaveContact(c store.Contact) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.SaveContact(c)
}
func (a *App) DeleteContact(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.DeleteContact(id)
}
func (a *App) History() ([]store.History, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.History()
}
func (a *App) ClearHistory() error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.ClearHistory()
}
func (a *App) Messages(account, remote string) ([]store.Message, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.Messages(account, a.phone.ConversationAddress(account, remote))
}
func (a *App) SendMessage(account, remote, body string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Message(account, remote, body)
}
func (a *App) ClearMessages() error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.ClearMessages()
}
func (a *App) ImportContactsCSV(text string) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	return a.store.ImportCSV(text)
}
func (a *App) ExportContactsCSV() (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.store.ExportCSV()
}

func (a *App) WatchPresence(account, target string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Watch(account, target)
}
func (a *App) PublishStatus(account string, available bool, note string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.PublishStatus(account, available, note)
}
func (a *App) TestAudio(settings media.Settings, mode string) (audio.TestResult, error) {
	if err := a.ready(); err != nil {
		return audio.TestResult{}, err
	}
	if len(a.phone.Snapshot().Calls) > 0 {
		return audio.TestResult{}, errors.New("end active calls before testing devices")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 6*time.Second)
	defer cancel()
	return media.TestAudio(ctx, settings, mode, 5*time.Second)
}

func (a *App) AttendedTransfer(id, consultation string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.AttendedTransfer(id, consultation)
}
func (a *App) Conference(ids []string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Conference(ids)
}
func (a *App) LeaveConference() error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.LeaveConference()
}
func (a *App) SetGain(id string, input, output float64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.SetGain(id, input, output)
}
func (a *App) ImportContactsVCard(text string) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	return a.store.ImportVCard(text)
}
func (a *App) ExportContactsVCard() (string, error) {
	if err := a.ready(); err != nil {
		return "", err
	}
	return a.store.ExportVCard()
}

func (a *App) LookupDirectory(cfg store.DirectoryConfig) (store.DirectoryResult, error) {
	if err := a.ready(); err != nil {
		return store.DirectoryResult{}, err
	}
	cfg, err := a.directory.Credentials(a.ctx, cfg)
	if err != nil {
		return store.DirectoryResult{}, err
	}
	return store.LookupDirectory(a.ctx, cfg)
}
func (a *App) ImportDirectory(contacts []store.Contact) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	return a.store.ImportDirectory(contacts)
}

func (a *App) WatchVoicemail(account string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.WatchVoicemail(account)
}
func (a *App) StartRecording(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Record call", DefaultFilename: "voiper-" + time.Now().Format("20060102-150405") + ".wav", Filters: []runtime.FileFilter{{DisplayName: "WAV audio", Pattern: "*.wav"}}})
	if err != nil || path == "" {
		return err
	}
	return a.phone.StartRecording(id, path)
}
func (a *App) StopRecording(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.StopRecording(id)
}

func (a *App) TakeDialTarget() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	target := a.dialTarget
	a.dialTarget = ""
	return target
}
func (a *App) receiveURI(target string) {
	a.mu.Lock()
	a.dialTarget = target
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.WindowShow(ctx)
		runtime.EventsEmit(ctx, "dial-target", nil)
	}
}
func dialTarget(argument string) (string, error) {
	if len(argument) > 2048 || strings.ContainsAny(argument, "\r\n\x00") {
		return "", errors.New("invalid call address")
	}
	scheme, value, found := strings.Cut(argument, ":")
	if !found {
		return "", errors.New("expected a call URI")
	}
	switch strings.ToLower(scheme) {
	case "sip", "sips":
		if value == "" {
			return "", errors.New("call address is empty")
		}
		return strings.ToLower(scheme) + ":" + value, nil
	case "tel":
		number := strings.NewReplacer("(", "", ")", "", ".", "", "-", "", " ", "").Replace(value)
		hasDigit := false
		for _, r := range number {
			if !strings.ContainsRune("0123456789+*#", r) {
				return "", errors.New("unsupported telephone URI")
			}
			if r >= '0' && r <= '9' {
				hasDigit = true
			}
		}
		if !hasDigit {
			return "", errors.New("telephone number is empty")
		}
		return number, nil
	}

	return "", errors.New("expected a sip:, sips:, or tel: URI")
}
func (a *App) DialVoicemail(account string) error {
	return a.startCall(func() error { return a.phone.DialVoicemail(account) })
}

func (a *App) Redirect(id, target string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.Redirect(id, target)
}

func (a *App) RetryAudio(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.RetryAudio(id)
}

func (a *App) RestartMediaPath(id string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.phone.RestartMediaPath(id)
}

func (a *App) DiscoverDirectory(cfg store.DirectoryConfig) ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	cfg, err := a.directory.Credentials(a.ctx, cfg)
	if err != nil {
		return nil, err
	}
	return store.DiscoverDirectory(a.ctx, cfg)
}

func (a *App) DialFeature(account, id, name, number string) error {
	return a.startCall(func() error { return a.phone.DialFeature(account, id, name, number) })
}
