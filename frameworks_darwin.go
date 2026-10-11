package main

// Native file dialogs use UTType on macOS 11 and later.
// Link it explicitly when building with a macOS 11 deployment target.
/*
#cgo LDFLAGS: -framework UniformTypeIdentifiers
*/
import "C"
