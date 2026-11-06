export namespace app {
	
	export class Preferences {
	    DefaultAccount: string;
	    Notifications: boolean;
	    CloseToBackground: boolean;
	    Autostart: boolean;
	    MessageRetentionDays: number;
	    KeepStatusWhenLocked: boolean;
	    ScreenLockCommand: string;
	
	    static createFrom(source: any = {}) {
	        return new Preferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DefaultAccount = source["DefaultAccount"];
	        this.Notifications = source["Notifications"];
	        this.CloseToBackground = source["CloseToBackground"];
	        this.Autostart = source["Autostart"];
	        this.MessageRetentionDays = source["MessageRetentionDays"];
	        this.KeepStatusWhenLocked = source["KeepStatusWhenLocked"];
	        this.ScreenLockCommand = source["ScreenLockCommand"];
	    }
	}
	export class DesktopSettings {
	    Preferences: Preferences;
	    Capabilities: desktop.Capabilities;
	
	    static createFrom(source: any = {}) {
	        return new DesktopSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Preferences = this.convertValues(source["Preferences"], Preferences);
	        this.Capabilities = this.convertValues(source["Capabilities"], desktop.Capabilities);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace audio {
	
	export class Device {
	    ID: string;
	    Name: string;
	    Kind: string;
	    Backend: string;
	
	    static createFrom(source: any = {}) {
	        return new Device(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Kind = source["Kind"];
	        this.Backend = source["Backend"];
	    }
	}
	export class TestResult {
	    Backend: string;
	    InputPeak: number;
	    OutputPeak: number;
	    CaptureOverruns: number;
	    PlaybackUnderruns: number;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Backend = source["Backend"];
	        this.InputPeak = source["InputPeak"];
	        this.OutputPeak = source["OutputPeak"];
	        this.CaptureOverruns = source["CaptureOverruns"];
	        this.PlaybackUnderruns = source["PlaybackUnderruns"];
	    }
	}

}

export namespace config {
	
	export class FeatureCode {
	    Name: string;
	    Target: string;
	    Action: string;
	
	    static createFrom(source: any = {}) {
	        return new FeatureCode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Target = source["Target"];
	        this.Action = source["Action"];
	    }
	}
	export class ICEServer {
	    URLs: string[];
	    Username: string;
	    Credential: string;
	
	    static createFrom(source: any = {}) {
	        return new ICEServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.URLs = source["URLs"];
	        this.Username = source["Username"];
	        this.Credential = source["Credential"];
	    }
	}
	export class Config {
	    SymmetricRTP: boolean;
	    DelayedOffer: boolean;
	    ICEPolicy: string;
	    ICEServers: ICEServer[];
	    MaxRedirects: number;
	    ForwardAlways: string;
	    ForwardBusy: string;
	    ForwardNoAnswer: string;
	    NoAnswerSeconds: number;
	    Features: FeatureCode[];
	    Codecs: string[];
	    TLSCAFile: string;
	    AutoEnable: boolean;
	    UseSecretService: boolean;
	    MediaSecurity: string;
	    Server: string;
	    Port: number;
	    Username: string;
	    Password: string;
	    DisplayName: string;
	    Domain: string;
	    AuthUsername: string;
	    Transport: string;
	    LocalAddress: string;
	    MediaAddress: string;
	    OutboundProxy: string;
	    PresenceMode: string;
	    MessagingMode: string;
	    DTMFMode: string;
	    Voicemail: string;
	    SwyxUsername: string;
	    SwyxPassword: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SymmetricRTP = source["SymmetricRTP"];
	        this.DelayedOffer = source["DelayedOffer"];
	        this.ICEPolicy = source["ICEPolicy"];
	        this.ICEServers = this.convertValues(source["ICEServers"], ICEServer);
	        this.MaxRedirects = source["MaxRedirects"];
	        this.ForwardAlways = source["ForwardAlways"];
	        this.ForwardBusy = source["ForwardBusy"];
	        this.ForwardNoAnswer = source["ForwardNoAnswer"];
	        this.NoAnswerSeconds = source["NoAnswerSeconds"];
	        this.Features = this.convertValues(source["Features"], FeatureCode);
	        this.Codecs = source["Codecs"];
	        this.TLSCAFile = source["TLSCAFile"];
	        this.AutoEnable = source["AutoEnable"];
	        this.UseSecretService = source["UseSecretService"];
	        this.MediaSecurity = source["MediaSecurity"];
	        this.Server = source["Server"];
	        this.Port = source["Port"];
	        this.Username = source["Username"];
	        this.Password = source["Password"];
	        this.DisplayName = source["DisplayName"];
	        this.Domain = source["Domain"];
	        this.AuthUsername = source["AuthUsername"];
	        this.Transport = source["Transport"];
	        this.LocalAddress = source["LocalAddress"];
	        this.MediaAddress = source["MediaAddress"];
	        this.OutboundProxy = source["OutboundProxy"];
	        this.PresenceMode = source["PresenceMode"];
	        this.MessagingMode = source["MessagingMode"];
	        this.DTMFMode = source["DTMFMode"];
	        this.Voicemail = source["Voicemail"];
	        this.SwyxUsername = source["SwyxUsername"];
	        this.SwyxPassword = source["SwyxPassword"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

export namespace desktop {
	
	export class Capabilities {
	    Tray: boolean;
	    Notifications: boolean;
	    NotificationActions: boolean;
	    SuspendMonitor: boolean;
	    NetworkMonitor: boolean;
	    Secrets: boolean;
	    Warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Tray = source["Tray"];
	        this.Notifications = source["Notifications"];
	        this.NotificationActions = source["NotificationActions"];
	        this.SuspendMonitor = source["SuspendMonitor"];
	        this.NetworkMonitor = source["NetworkMonitor"];
	        this.Secrets = source["Secrets"];
	        this.Warnings = source["Warnings"];
	    }
	}
	export class ShortcutBinding {
	    ID: string;
	    Description: string;
	    Trigger: string;
	
	    static createFrom(source: any = {}) {
	        return new ShortcutBinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Description = source["Description"];
	        this.Trigger = source["Trigger"];
	    }
	}
	export class ShortcutSettings {
	    Available: boolean;
	    Enabled: boolean;
	    Configuring: boolean;
	    CanConfigure: boolean;
	    Bindings: ShortcutBinding[];
	    Error: string;
	
	    static createFrom(source: any = {}) {
	        return new ShortcutSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Available = source["Available"];
	        this.Enabled = source["Enabled"];
	        this.Configuring = source["Configuring"];
	        this.CanConfigure = source["CanConfigure"];
	        this.Bindings = this.convertValues(source["Bindings"], ShortcutBinding);
	        this.Error = source["Error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace directory {
	
	export class State {
	    Profile: store.DirectoryProfile;
	    Status: store.DirectoryStatus;
	    Running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Profile = this.convertValues(source["Profile"], store.DirectoryProfile);
	        this.Status = this.convertValues(source["Status"], store.DirectoryStatus);
	        this.Running = source["Running"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace media {
	
	export class Settings {
	    SymmetricRTP: boolean;
	    ICEPolicy: string;
	    ICEServers: rtp.ICEServer[];
	    EchoCancellation: boolean;
	    NoiseSuppression: boolean;
	    DisableAutoRecovery: boolean;
	    MediaSecurity: string;
	    SecureSignaling: boolean;
	    InputDevice: string;
	    OutputDevice: string;
	    RingerDevice: string;
	    Backend: string;
	    BindAddress: string;
	    Codecs: string[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SymmetricRTP = source["SymmetricRTP"];
	        this.ICEPolicy = source["ICEPolicy"];
	        this.ICEServers = this.convertValues(source["ICEServers"], rtp.ICEServer);
	        this.EchoCancellation = source["EchoCancellation"];
	        this.NoiseSuppression = source["NoiseSuppression"];
	        this.DisableAutoRecovery = source["DisableAutoRecovery"];
	        this.MediaSecurity = source["MediaSecurity"];
	        this.SecureSignaling = source["SecureSignaling"];
	        this.InputDevice = source["InputDevice"];
	        this.OutputDevice = source["OutputDevice"];
	        this.RingerDevice = source["RingerDevice"];
	        this.Backend = source["Backend"];
	        this.BindAddress = source["BindAddress"];
	        this.Codecs = source["Codecs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Stats {
	    MediaFlow: string;
	    MediaWarning: string;
	    SendIdleSeconds: number;
	    ReceiveIdleSeconds: number;
	    EncoderBitrate: number;
	    RemotePacketLossPercent: number;
	    ReceiverReportAgeSeconds: number;
	    ReceiverReportAvailable: boolean;
	    CleanupPending: boolean;
	    ICERestartPending: boolean;
	    ICEState: string;
	    ICECandidateType: string;
	    RTCPMux: boolean;
	    AudioRecoveryPending: boolean;
	    AudioRecovering: boolean;
	    AudioRecoveryAttempts: number;
	    AudioRecoveryError: string;
	    DSPAvailable: boolean;
	    EchoCancellation: boolean;
	    NoiseSuppression: boolean;
	    EarlyMedia: boolean;
	    AudioStopped: boolean;
	    Recording: boolean;
	    RecordingPath: string;
	    Conference: boolean;
	    InputGain: number;
	    OutputGain: number;
	    InputLevel: number;
	    OutputLevel: number;
	    Codec: string;
	    SampleRate: number;
	    ClockRate: number;
	    DeviceSampleRate: number;
	    PacketizationMilliseconds: number;
	    Transport: string;
	    Encrypted: boolean;
	    LocalAddress: string;
	    RemoteAddress: string;
	    PacketsSent: number;
	    PacketsReceived: number;
	    PacketsLost: number;
	    PacketsDropped: number;
	    BytesSent: number;
	    BytesReceived: number;
	    JitterMilliseconds: number;
	    RTTMilliseconds: number;
	    BufferMilliseconds: number;
	    CaptureOverruns: number;
	    PlaybackUnderruns: number;
	    Muted: boolean;
	    Held: boolean;
	    LastError: string;
	
	    static createFrom(source: any = {}) {
	        return new Stats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.MediaFlow = source["MediaFlow"];
	        this.MediaWarning = source["MediaWarning"];
	        this.SendIdleSeconds = source["SendIdleSeconds"];
	        this.ReceiveIdleSeconds = source["ReceiveIdleSeconds"];
	        this.EncoderBitrate = source["EncoderBitrate"];
	        this.RemotePacketLossPercent = source["RemotePacketLossPercent"];
	        this.ReceiverReportAgeSeconds = source["ReceiverReportAgeSeconds"];
	        this.ReceiverReportAvailable = source["ReceiverReportAvailable"];
	        this.CleanupPending = source["CleanupPending"];
	        this.ICERestartPending = source["ICERestartPending"];
	        this.ICEState = source["ICEState"];
	        this.ICECandidateType = source["ICECandidateType"];
	        this.RTCPMux = source["RTCPMux"];
	        this.AudioRecoveryPending = source["AudioRecoveryPending"];
	        this.AudioRecovering = source["AudioRecovering"];
	        this.AudioRecoveryAttempts = source["AudioRecoveryAttempts"];
	        this.AudioRecoveryError = source["AudioRecoveryError"];
	        this.DSPAvailable = source["DSPAvailable"];
	        this.EchoCancellation = source["EchoCancellation"];
	        this.NoiseSuppression = source["NoiseSuppression"];
	        this.EarlyMedia = source["EarlyMedia"];
	        this.AudioStopped = source["AudioStopped"];
	        this.Recording = source["Recording"];
	        this.RecordingPath = source["RecordingPath"];
	        this.Conference = source["Conference"];
	        this.InputGain = source["InputGain"];
	        this.OutputGain = source["OutputGain"];
	        this.InputLevel = source["InputLevel"];
	        this.OutputLevel = source["OutputLevel"];
	        this.Codec = source["Codec"];
	        this.SampleRate = source["SampleRate"];
	        this.ClockRate = source["ClockRate"];
	        this.DeviceSampleRate = source["DeviceSampleRate"];
	        this.PacketizationMilliseconds = source["PacketizationMilliseconds"];
	        this.Transport = source["Transport"];
	        this.Encrypted = source["Encrypted"];
	        this.LocalAddress = source["LocalAddress"];
	        this.RemoteAddress = source["RemoteAddress"];
	        this.PacketsSent = source["PacketsSent"];
	        this.PacketsReceived = source["PacketsReceived"];
	        this.PacketsLost = source["PacketsLost"];
	        this.PacketsDropped = source["PacketsDropped"];
	        this.BytesSent = source["BytesSent"];
	        this.BytesReceived = source["BytesReceived"];
	        this.JitterMilliseconds = source["JitterMilliseconds"];
	        this.RTTMilliseconds = source["RTTMilliseconds"];
	        this.BufferMilliseconds = source["BufferMilliseconds"];
	        this.CaptureOverruns = source["CaptureOverruns"];
	        this.PlaybackUnderruns = source["PlaybackUnderruns"];
	        this.Muted = source["Muted"];
	        this.Held = source["Held"];
	        this.LastError = source["LastError"];
	    }
	}

}

export namespace phone {
	
	export class AccountState {
	    PresenceState: string;
	    PresenceNote: string;
	    PresenceError: string;
	    Features: config.FeatureCode[];
	    Forwarding: boolean;
	    Name: string;
	    State: string;
	    Error: string;
	    Capabilities: swyx.Capabilities;
	    SwyxAPI: string;
	    SwyxAPIError: string;
	
	    static createFrom(source: any = {}) {
	        return new AccountState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.PresenceState = source["PresenceState"];
	        this.PresenceNote = source["PresenceNote"];
	        this.PresenceError = source["PresenceError"];
	        this.Features = this.convertValues(source["Features"], config.FeatureCode);
	        this.Forwarding = source["Forwarding"];
	        this.Name = source["Name"];
	        this.State = source["State"];
	        this.Error = source["Error"];
	        this.Capabilities = this.convertValues(source["Capabilities"], swyx.Capabilities);
	        this.SwyxAPI = source["SwyxAPI"];
	        this.SwyxAPIError = source["SwyxAPIError"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CallState {
	    ID: string;
	    Account: string;
	    Remote: string;
	    Direction: string;
	    State: string;
	    Error: string;
	    TransferStatus: string;
	    ConsultationFor: string;
	    Muted: boolean;
	    Held: boolean;
	    // Go type: time
	    Started: any;
	    // Go type: time
	    Connected: any;
	    Stats: media.Stats;
	
	    static createFrom(source: any = {}) {
	        return new CallState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Account = source["Account"];
	        this.Remote = source["Remote"];
	        this.Direction = source["Direction"];
	        this.State = source["State"];
	        this.Error = source["Error"];
	        this.TransferStatus = source["TransferStatus"];
	        this.ConsultationFor = source["ConsultationFor"];
	        this.Muted = source["Muted"];
	        this.Held = source["Held"];
	        this.Started = this.convertValues(source["Started"], null);
	        this.Connected = this.convertValues(source["Connected"], null);
	        this.Stats = this.convertValues(source["Stats"], media.Stats);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Voicemail {
	    Account: string;
	    State: string;
	    Waiting: boolean;
	    New: number;
	    Old: number;
	    UrgentNew: number;
	    UrgentOld: number;
	    // Go type: time
	    Updated: any;
	
	    static createFrom(source: any = {}) {
	        return new Voicemail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Account = source["Account"];
	        this.State = source["State"];
	        this.Waiting = source["Waiting"];
	        this.New = source["New"];
	        this.Old = source["Old"];
	        this.UrgentNew = source["UrgentNew"];
	        this.UrgentOld = source["UrgentOld"];
	        this.Updated = this.convertValues(source["Updated"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Presence {
	    Target: string;
	    Account: string;
	    Remote: string;
	    State: string;
	    Note: string;
	    Source: string;
	    SubscriptionID: string;
	    // Go type: time
	    Updated: any;
	    ""?: Voicemail;
	
	    static createFrom(source: any = {}) {
	        return new Presence(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Target = source["Target"];
	        this.Account = source["Account"];
	        this.Remote = source["Remote"];
	        this.State = source["State"];
	        this.Note = source["Note"];
	        this.Source = source["Source"];
	        this.SubscriptionID = source["SubscriptionID"];
	        this.Updated = this.convertValues(source["Updated"], null);
	        this[""] = this.convertValues(source[""], Voicemail);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Snapshot {
	    UnreadMessages: number;
	    Accounts: AccountState[];
	    Calls: CallState[];
	    DND: boolean;
	    ScreenLocked: boolean;
	    Audio: media.Settings;
	    Presence: Presence[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.UnreadMessages = source["UnreadMessages"];
	        this.Accounts = this.convertValues(source["Accounts"], AccountState);
	        this.Calls = this.convertValues(source["Calls"], CallState);
	        this.DND = source["DND"];
	        this.ScreenLocked = source["ScreenLocked"];
	        this.Audio = this.convertValues(source["Audio"], media.Settings);
	        this.Presence = this.convertValues(source["Presence"], Presence);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace rtp {
	
	export class ICEServer {
	    URLs: string[];
	    Username: string;
	    Credential: string;
	
	    static createFrom(source: any = {}) {
	        return new ICEServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.URLs = source["URLs"];
	        this.Username = source["Username"];
	        this.Credential = source["Credential"];
	    }
	}

}

export namespace store {
	
	export class CSVNumberColumn {
	    Column: string;
	    Label: string;
	
	    static createFrom(source: any = {}) {
	        return new CSVNumberColumn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Column = source["Column"];
	        this.Label = source["Label"];
	    }
	}
	export class CSVMapping {
	    Name: string;
	    AdditionalName: string;
	    Address: string;
	    Notes: string;
	    Numbers: CSVNumberColumn[];
	
	    static createFrom(source: any = {}) {
	        return new CSVMapping(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.AdditionalName = source["AdditionalName"];
	        this.Address = source["Address"];
	        this.Notes = source["Notes"];
	        this.Numbers = this.convertValues(source["Numbers"], CSVNumberColumn);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ContactNumber {
	    Label: string;
	    Address: string;
	
	    static createFrom(source: any = {}) {
	        return new ContactNumber(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Label = source["Label"];
	        this.Address = source["Address"];
	    }
	}
	export class Contact {
	    ID: number;
	    Name: string;
	    Company: string;
	    Address: string;
	    Numbers: ContactNumber[];
	    Notes: string;
	    Favorite: boolean;
	    Account: string;
	    Source: string;
	    DirectoryID: string;
	    // Go type: time
	    Updated: any;
	
	    static createFrom(source: any = {}) {
	        return new Contact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Company = source["Company"];
	        this.Address = source["Address"];
	        this.Numbers = this.convertValues(source["Numbers"], ContactNumber);
	        this.Notes = source["Notes"];
	        this.Favorite = source["Favorite"];
	        this.Account = source["Account"];
	        this.Source = source["Source"];
	        this.DirectoryID = source["DirectoryID"];
	        this.Updated = this.convertValues(source["Updated"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ContactImportEntry {
	    Record: number;
	    Contact: Contact;
	    Action: string;
	    Detail: string;
	
	    static createFrom(source: any = {}) {
	        return new ContactImportEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Record = source["Record"];
	        this.Contact = this.convertValues(source["Contact"], Contact);
	        this.Action = source["Action"];
	        this.Detail = source["Detail"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ContactImportPreview {
	    Entries: ContactImportEntry[];
	    Added: number;
	    Updated: number;
	    Skipped: number;
	    Invalid: number;
	    Committed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ContactImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Entries = this.convertValues(source["Entries"], ContactImportEntry);
	        this.Added = source["Added"];
	        this.Updated = source["Updated"];
	        this.Skipped = source["Skipped"];
	        this.Invalid = source["Invalid"];
	        this.Committed = source["Committed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Conversation {
	    Account: string;
	    Remote: string;
	    Preview: string;
	    Status: string;
	    // Go type: time
	    Updated: any;
	    Unread: number;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Account = source["Account"];
	        this.Remote = source["Remote"];
	        this.Preview = source["Preview"];
	        this.Status = source["Status"];
	        this.Updated = this.convertValues(source["Updated"], null);
	        this.Unread = source["Unread"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DirectoryConfig {
	    URL: string;
	    BaseDN: string;
	    BindDN: string;
	    Password: string;
	    CAFile: string;
	    Limit: number;
	
	    static createFrom(source: any = {}) {
	        return new DirectoryConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.URL = source["URL"];
	        this.BaseDN = source["BaseDN"];
	        this.BindDN = source["BindDN"];
	        this.Password = source["Password"];
	        this.CAFile = source["CAFile"];
	        this.Limit = source["Limit"];
	    }
	}
	export class DirectoryProfile {
	    URL: string;
	    BaseDN: string;
	    BindDN: string;
	    CAFile: string;
	    Limit: number;
	    IntervalMinutes: number;
	    Enabled: boolean;
	    UseSecretService: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DirectoryProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.URL = source["URL"];
	        this.BaseDN = source["BaseDN"];
	        this.BindDN = source["BindDN"];
	        this.CAFile = source["CAFile"];
	        this.Limit = source["Limit"];
	        this.IntervalMinutes = source["IntervalMinutes"];
	        this.Enabled = source["Enabled"];
	        this.UseSecretService = source["UseSecretService"];
	    }
	}
	export class DirectoryResult {
	    Contacts: Contact[];
	    Truncated: boolean;
	    Skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new DirectoryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Contacts = this.convertValues(source["Contacts"], Contact);
	        this.Truncated = source["Truncated"];
	        this.Skipped = source["Skipped"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DirectoryStatus {
	    // Go type: time
	    LastAttempt: any;
	    // Go type: time
	    LastSuccess: any;
	    Error: string;
	    Entries: number;
	    Preserved: number;
	    Partial: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DirectoryStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.LastAttempt = this.convertValues(source["LastAttempt"], null);
	        this.LastSuccess = this.convertValues(source["LastSuccess"], null);
	        this.Error = source["Error"];
	        this.Entries = source["Entries"];
	        this.Preserved = source["Preserved"];
	        this.Partial = source["Partial"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class History {
	    ID: number;
	    Account: string;
	    Remote: string;
	    Direction: string;
	    Status: string;
	    // Go type: time
	    Started: any;
	    // Go type: time
	    Ended: any;
	
	    static createFrom(source: any = {}) {
	        return new History(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Account = source["Account"];
	        this.Remote = source["Remote"];
	        this.Direction = source["Direction"];
	        this.Status = source["Status"];
	        this.Started = this.convertValues(source["Started"], null);
	        this.Ended = this.convertValues(source["Ended"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Message {
	    ID: number;
	    Account: string;
	    Remote: string;
	    Body: string;
	    Direction: string;
	    Status: string;
	    // Go type: time
	    Created: any;
	    Read: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Account = source["Account"];
	        this.Remote = source["Remote"];
	        this.Body = source["Body"];
	        this.Direction = source["Direction"];
	        this.Status = source["Status"];
	        this.Created = this.convertValues(source["Created"], null);
	        this.Read = source["Read"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace swyx {
	
	export class Capabilities {
	    Server: string;
	    SwyxDetected: boolean;
	    Presence: string;
	    Messaging: string;
	    Status: string;
	    Directory: string;
	    Detail: string;
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Server = source["Server"];
	        this.SwyxDetected = source["SwyxDetected"];
	        this.Presence = source["Presence"];
	        this.Messaging = source["Messaging"];
	        this.Status = source["Status"];
	        this.Directory = source["Directory"];
	        this.Detail = source["Detail"];
	    }
	}

}

