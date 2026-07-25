package setup

import "testing"

func TestBotPermissionsKnownBits(t *testing.T) {
	// Sanity-check the packed bitfield includes expected low bits.
	const (
		viewChannels         = 1 << 10
		sendMessages         = 1 << 11
		embedLinks           = 1 << 14
		attachFiles          = 1 << 15
		readMessageHistory   = 1 << 16
		createPublicThreads  = 1 << 35
		sendMessagesInThread = 1 << 38
	)
	want := viewChannels | sendMessages | embedLinks | attachFiles |
		readMessageHistory | createPublicThreads | sendMessagesInThread
	if botPermissions != want {
		t.Fatalf("botPermissions = %d, want %d", botPermissions, want)
	}
}
