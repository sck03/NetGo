package model

import "testing"

func TestVersionContract(t *testing.T) {
	for _, v := range []string{"0.3.0-dev", "1.2.3-rc.4+ci.9", "65535.0.0+001", "1.2.3-0"} {
		if !ValidVersion(v) {
			t.Errorf("rejected %q", v)
		}
	}
	for _, v := range []string{"test", "1.2.3+", "1.2.3-01", "01.2.3", "65536.0.0", "1.2.3-rc..1", "1.2.3+ci..9", "../1.2.3", "v1.2.3"} {
		if ValidVersion(v) {
			t.Errorf("accepted %q", v)
		}
	}
}
