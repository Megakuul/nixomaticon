package media

import "errors"

func (c *Call) validateDTLS(remote description, offer bool) error {
	if !c.dtls {
		if remote.fingerprint != "" {
			return errors.New("DTLS-SRTP was not offered")
		}
		return nil
	}
	if remote.fingerprint == "" || remote.dtlsProfile != c.dtlsProfile {
		return errors.New("peer changed the required DTLS-SRTP profile")
	}
	if !offer && remote.setup == "actpass" {
		return errors.New("DTLS answer must select active or passive setup")
	}
	if c.connected || c.early {
		if c.iceDescription.Username == "" && remote.address.String() != c.remote.address.String() {
			return errors.New("changing DTLS media addresses without ICE requires a new call")
		}
		if remote.tlsID != c.remote.tlsID || remote.fingerprintSet != c.remote.fingerprintSet {
			return errors.New("changing the DTLS fingerprint or association identity requires a new call")
		}
		if (c.dtlsSetup == "active" && remote.setup == "active") || (c.dtlsSetup == "passive" && remote.setup == "passive") {
			return errors.New("changing the DTLS setup role requires a new call")
		}
	}
	return nil
}
