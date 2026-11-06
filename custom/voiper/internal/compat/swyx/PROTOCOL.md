# Swyx Classic presence evidence

The interoperability reference is [simcrack/twinkle, swyx-support](https://github.com/simcrack/twinkle/tree/8d583984134b04f0b01ddf659e4565848dc0f954), inspected at commit `8d583984134b04f0b01ddf659e4565848dc0f954`. The adapter is an independent Go implementation of the observed protocol fields; no Twinkle source or assets are incorporated.

`src/presence/pidf_body.h` and `pidf_body.cpp` describe a PIDF `presence/tuple/status` child named `userstatus` in namespace `http://sip.lanphone.de/presence/`. Prefix spelling is irrelevant. The wire values map as follows:

| Wire value | Voiper state |
| --- | --- |
| `logged on` | available |
| `logged off` | offline |
| `active` | in-call |
| `away` | away |
| `donotdisturb` | dnd |

`presence_subscription.cpp` receives this in ordinary `Event: presence` subscriptions and `application/pidf+xml` NOTIFY bodies. The extension takes precedence over PIDF basic open/closed. Unknown extension values and conflicting tuples produce unknown; an absent extension allows the application to use standard PIDF. The adapter does not infer state from a reachable server. Subscription authorization, sender/account mapping, refresh, termination, and timestamps remain owned by the SIP/application layers.

The branch adds reception/display of these statuses, but supplies no verified vendor status-publication exchange. Its diff against the reference master has no messaging implementation changes, custom MESSAGE content type, Messenger authentication, or directory API. Ordinary SIP MESSAGE remains the baseline; a Swyx server banner is insufficient evidence to select a proprietary messaging transport. Modern Swyx Messenger interoperability remains unverified.

Tests construct synthetic PIDF fixtures from these protocol facts. They demonstrate parsing and namespace isolation, not a live SwyxWare certification or support for all server generations.

## Additional interoperability research (2026-10-07)

A Classic client does not identify a single messaging generation. Enreach's [product history](https://service.swyx.net/hc/en-gb/articles/360011605939-SwyxWare-Product-History) dates the original messenger to 6.12 and the replacement messenger to 12.10. The [Classic manual](https://help.enreach.com/docs/manuals/english/SwyxIt%21_classic.pdf), chapter 11, explicitly describes launching the newer Messenger from Classic. Neither the supplied Twinkle branch nor the examined public SDK material specifies the older chat wire payload; its interoperability must not be inferred from a SIP banner or an `Allow: MESSAGE` header.

The official [Swyx 14 port matrix](https://service.swyx.net/hc/en-gb/articles/14020952752540-Which-Ports-are-used-by-Swyx-14) identifies modern Messenger's SwyxWare HTTPS API on port 9100, a separate HTTPS token service, Coligo chat over secure WebSockets, and local SignalR communication with SwyxIt!. These transport names and addresses are insufficient to implement authentication, message identity, acknowledgements, or synchronization. Voiper therefore does not probe those services or send SIP passwords to them. The supported SIP messenger remains a separate transport, not a claim of compatibility with the Coligo service.

The public [CDS SDK guide](https://cdssdk.swyx.engineering/guide/index.html) distinguishes the .NET/WCF API from a recommended client REST API. Its [.NET server facade](https://cdssdk.swyx.engineering/api/SWConfigDataClientLib.Proxies.IppbxServer.IppbxServerFacade.html) documents `SetUserPresenceData` with user ID, away/DND flags and expiration, but that does not define a SIP publication body. The [REST getting-started guide](https://cdssdk.swyx.engineering/guide/cdsrest-getting-started.html) explicitly states that public REST documentation is unavailable and directs administrators to enable Swagger on their installed server. The version-specific schema is available at `https://localhost:9100/ippbx/swagger` after that administrator action. No running Swyx server or schema was available during implementation. A sanitized schema and authenticated protocol fixtures are the concrete missing inputs for a portable status-publication or Messenger adapter; guessing REST routes or replaying undocumented XML would not establish support.

For directories, Enreach documents [AD LDS with one-way TLS](https://service.swyx.net/hc/en-gb/articles/13860542950940-Swyx-Phonebook-Does-Not-Longer-Work-with-Older-Yealink-Phones-T4x-CP9x0) for the global phonebook and a separately configured [LDAP password in provisioning settings](https://help.enreach.com/controlcenter/14.25/web/Swyx/en-US/help/chap_serverconfiguration.06.11.html). SIP authentication does not provide that password. Voiper's explicit LDAP/LDAPS configuration uses verified TLS and can discover advertised naming contexts through the standard [LDAP RootDSE](https://www.rfc-editor.org/rfc/rfc4512#section-5.1). It does not guess server endpoints, scan extensions, or copy provisioning credentials. Classic's optional VisualContacts integration also supports separately administered LDAP directories, including [XPhone Connect Directory](https://help.c4b.com/xphone-connect-10/doc/en/admin/intgrtn/clnt/swyx.html). LDAP entries retain multiple labeled numbers; identical display names do not combine different people.

Receiving the tested Classic PIDF extension is supported. Proprietary publication and both unverified vendor messaging generations remain explicitly unavailable until their actual service contracts can be tested. This distinction is deliberate protocol correctness, not a simulated implementation. *(Superseded for status publication by the [live server probe](#live-server-probe-2026-10-15), which found the client API schema on the target server.)*

## Downloadable SDK and client-contract audit (2026-10-07)

The second pass downloaded and inspected the vendor artifacts themselves, rather
than relying only on the landing pages:

- [CDSClientSDK 12.10](https://www.swyxdownload.com/download/CDSClientSDK_v12.10.0.0.zip):
  `Documentation/Readme.pdf`, `BestPractice.pdf`, the sample `CDSLib.cs` and
  application configuration. Authentication is delegated to `LibManager` with
  a SwyxIt login or Windows identity. Samples call .NET facade methods; they do
  not define an HTTP status/chat wire exchange.
- [Swyx.ConfigDataStoreClient.Managed 12.23.5](https://www.nuget.org/packages/Swyx.ConfigDataStoreClient.Managed/12.23.5)
  and its Common package: the published XML documentation exposes presence,
  phonebook and configuration operations. Assembly metadata identifies
  `NetTcpBinding`, `.wnd`, `.upwd`, `.wupwd` and `.jwt2` service endpoints.
  The package targets .NET Framework 4.7.2 and depends on the vendor Common
  assemblies; the SDK additionally documents its native BLOB serializer.
  A facade signature therefore is not a SOAP-over-HTTPS request contract.
- [Swyx.Client.ClmgrAPI 14.21.0](https://www.nuget.org/packages/Swyx.Client.ClmgrAPI/14.21.0)
  contains x86/x64 COM interop assemblies. The official
  [ClientLineMgr interface](https://clientsdk-dev.sws.swyx.engineering/interface_i_client_line_mgr_disp-members.html)
  exposes `DispRegisterChatMessageReader`, `DispSendChatMessage`,
  `DispReadChatMessage`, and `DispAcknowledgeChatMessage`. Send/ack use a message
  identifier, peer name and peer IPv4 address. These are meaningful Classic chat
  operations, but they are calls to an installed Windows client, not a published
  SIP message framing or recipient-discovery specification.
- The official [Web Extension SDK](https://www.npmjs.com/package/@enreachde/swyx-web-extension-sdk)
  version 1.0.1 contains typed presence and phonebook APIs. Its README requires
  SwyxIt 15.00 or newer; its implementation connects to `localhost` and the
  `/webextension` hub. It is a local client extension and does not supply
  standalone PBX authentication, proprietary chat transport or a replacement for
  the Classic runtime.

The inspected Twinkle branch still changes only presence reception/display and
related UI/build files. Its added `set_user_status` accessor does not add a
vendor publication request or serialize a new `userstatus` element for upload.
No third-party wrapper source, COM declaration, or NuGet symbol is treated as
proof that an invented SIP `MESSAGE`/`PUBLISH` payload will work.

For practical directory integration there is stronger evidence:
[Swyx 13.27 release notes](https://service.swyx.net/hc/en-gb/articles/8820023075868-Swyx-13-27)
explicitly expose the LDAP reader password for third-party phonebook access.
The [versioned port matrix](https://service.swyx.net/hc/en-gb/articles/14020952752540-Which-Ports-are-used-by-Swyx-14)
lists LDAP 389 before 12.30 and LDAPS 636 from 12.30. The reader credentials and
server remain administrator-provided; a SIP registration does not return them.
Use the app's configured LDAP/LDAPS source and RootDSE base discovery, or import
CSV/vCard exports. No vendor DNS service label or unauthenticated provisioning
contract was established by this audit.

The remaining implementation inputs are precise: a server-version REST schema
and login/refresh contract for publication and directory service access; or a
Classic messaging trace/specification covering recipient resolution, MIME/body,
message identifiers and acknowledgements. A Windows CDS/COM bridge is an
available architectural alternative, but it would add a Windows runtime/server
component to the requested standalone Linux client. No such dependency is added.

## Live server probe (2026-10-15)

A SwyxWare installation was available for unauthenticated probing; no call was
placed and the only SIP request addressed to a person went to extension 100.

- `OPTIONS sip:<server>` and `sip:100@<server>` return `200 OK` with
  `User-Agent: Swyx IpPbxSrv/14.21 (Swyx.Core_14.21_20250617.1)` and
  `Allow: REGISTER, INVITE, ACK, CANCEL, BYE, NOTIFY, REFER, OPTIONS, INFO, SUBSCRIBE, UPDATE`.
  Responses carry the product in `User-Agent`, not `Server`.
- `MESSAGE sip:100@<server>` and `PUBLISH` of a PIDF document both return
  `405 Method Not Allowed`, before any authentication challenge. SIP chat and
  standard status publication are therefore not available on this server.
- TCP 9100 serves the "CDS Client REST API" (`/ippbx/client/v1.0`) with its
  Swagger 2.0 document at `/ippbx/swagger/swagger.json`. `login/apiversion`
  reports 1.0.6, `login/buildversion` 14.21. The server certificate is
  self-signed.

The operations Voiper uses, with the schema's own enum documentation:

| Operation | Contract |
| --- | --- |
| `POST login/acquiretoken` | Body `{UserName, Password}`; returns `{AccessToken, RefreshToken, UserId}`. Wrong credentials return 401 with `{ErrorDetails, SecurityErrorCode: "AuthenticationFailed"}`. |
| `POST login/refreshtoken?token=` | Returns a new `AuthenticationResult`. |
| `GET`/`PUT user/{userId}/presenceconfiguration` | `{IsAway, DoNotDisturb, AwayText, Expiration, AppointmentText, AppointmentExpiration, LoginDeviceType, ...}`; the PUT replaces the whole object, so Voiper reads, changes its fields and writes back. Dates use WCF `/Date(ms+zone)/` strings and pass through unchanged. |
| `GET employees/?paginationIndex=&paginationCount=` | `[{SiteId, EmployeeId, Name, Number, AllEmployeeNumbers: [{Number, NumberType}], Presence}]`; `NumberType` 0 public, 1 internal, 2 e-mail. |
| `GET employees/presence?clientTimestamp=` | `{Timstamp, List: [{SiteId, EmployeeId, Presence: {Status, StatusText, ...}}]}` (sic); the timestamp is passed back for changes only. `Status`: 0 logged off, 1 logged on, 2 active, 3 no status, 4 away, 5 do not disturb. |
| `GET user/{userId}` | Includes `MessagingSystem`: 0 Classic IM, 1 Coligo IM. |

Requests send `Authorization: bearer <AccessToken>`. The API contains no chat
send/receive operation; `chatconfiguration` holds only a `ChatUserId` for the
separate messenger. Swyx chat therefore remains unimplemented: neither Classic
IM nor the Coligo service has a published or observed wire contract.

Not yet observed live: an authenticated session (the probe had no credentials),
whether SIP and client-API passwords match on this installation, and how
SwyxIt! displays `AwayText` while not away.
