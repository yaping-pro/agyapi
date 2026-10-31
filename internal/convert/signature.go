package convert

import "strings"

// ThoughtSignatureSeparator delimits the original tool-call id from the
// upstream thoughtSignature that must be replayed on the next request.
//
// OpenAI clients drop unknown fields on round-trip, so the signature is
// smuggled inside the tool-call id, which clients do preserve verbatim.
const ThoughtSignatureSeparator = "__thought__"

// EncodeToolID appends the signature to the id. An empty signature is a no-op.
func EncodeToolID(id, signature string) string {
	if signature == "" {
		return id
	}
	return id + ThoughtSignatureSeparator + signature
}

// DecodeToolID splits an encoded id. Only the first separator is significant,
// because a signature may itself contain the separator sequence.
func DecodeToolID(encoded string) (id string, signature string) {
	if encoded == "" {
		return "", ""
	}
	idx := strings.Index(encoded, ThoughtSignatureSeparator)
	if idx < 0 {
		return encoded, ""
	}
	return encoded[:idx], encoded[idx+len(ThoughtSignatureSeparator):]
}
