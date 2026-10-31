package server

// Version is the build version reported by /health. Release builds override it
// with -ldflags "-X github.com/yaping-pro/agyapi/internal/server.Version=x.y.z".
// It stays a variable, not a constant, precisely so the linker can rewrite it.
var Version = "1.1.0"
