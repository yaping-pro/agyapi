package convert

import (
	"strings"
	"testing"
)

func TestEncodeDecodeThoughtSignatureRoundTrip(t *testing.T) {
	const (
		id  = "call_89342"
		sig = "EmcKZQFpFH0TYuFXRNatu6PLzhWmrw7HtUAc6WBQ"
	)
	encoded := EncodeToolID(id, sig)
	if !strings.HasPrefix(encoded, id+ThoughtSignatureSeparator) {
		t.Fatalf("encoded id = %q, want prefix %q", encoded, id+ThoughtSignatureSeparator)
	}

	gotID, gotSig := DecodeToolID(encoded)
	if gotID != id {
		t.Fatalf("id = %q, want %q", gotID, id)
	}
	if gotSig != sig {
		t.Fatalf("sig = %q, want %q", gotSig, sig)
	}
}

func TestEncodeToolIDWithoutSignatureIsIdentity(t *testing.T) {
	if got := EncodeToolID("call_1", ""); got != "call_1" {
		t.Fatalf("EncodeToolID = %q, want call_1", got)
	}
}

func TestDecodeToolIDPlainValue(t *testing.T) {
	id, sig := DecodeToolID("call_plain")
	if id != "call_plain" || sig != "" {
		t.Fatalf("DecodeToolID = (%q,%q), want (call_plain,\"\")", id, sig)
	}
}

func TestDecodeToolIDToleratesSignatureContainingSeparator(t *testing.T) {
	// The separator may legally appear inside a signature; only the first
	// occurrence splits id from signature.
	encoded := "call_1" + ThoughtSignatureSeparator + "a" + ThoughtSignatureSeparator + "b"
	id, sig := DecodeToolID(encoded)
	if id != "call_1" {
		t.Fatalf("id = %q, want call_1", id)
	}
	if sig != "a"+ThoughtSignatureSeparator+"b" {
		t.Fatalf("sig = %q, want whole remainder", sig)
	}
}

func TestDecodeToolIDEmptyInput(t *testing.T) {
	id, sig := DecodeToolID("")
	if id != "" || sig != "" {
		t.Fatalf("DecodeToolID(\"\") = (%q,%q), want empty", id, sig)
	}
}
