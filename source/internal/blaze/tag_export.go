package blaze

// EncodeTag exposes the Heat tag encoder (used by tests to check member order).
func EncodeTag(s string) [3]byte { return encodeTag(s) }
