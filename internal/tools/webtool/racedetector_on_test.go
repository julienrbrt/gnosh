//go:build race

package webtool

// raceDetector says the tests were built with the race detector, which
// makes this package's work several times slower.
const raceDetector = true
