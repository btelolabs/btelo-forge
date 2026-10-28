//go:build race

package ui

// raceBuild says the race detector is on: everything runs several times slower, so timings mean nothing.
const raceBuild = true
