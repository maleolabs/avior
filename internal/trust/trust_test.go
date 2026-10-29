package trust

import "testing"

func TestInstallOptionalTargetReportsInstalled(t *testing.T) {
	m := &TrustManager{}
	result := m.installOptionalTarget(TrustTargetFirefox, func() error { return nil })
	if len(result) != 1 || result[0].Target != TrustTargetFirefox || result[0].Status != TrustTargetInstalled || result[0].Error != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestInstallOptionalTargetReportsFailure(t *testing.T) {
	m := &TrustManager{}
	result := m.installOptionalTarget(TrustTargetChromiumNSS, func() error { return errTestTrust })
	if len(result) != 1 || result[0].Target != TrustTargetChromiumNSS || result[0].Status != TrustTargetFailed || result[0].Error != errTestTrust.Error() {
		t.Fatalf("unexpected result: %#v", result)
	}
}

var errTestTrust = testTrustError("certutil unavailable")

type testTrustError string

func (e testTrustError) Error() string { return string(e) }
