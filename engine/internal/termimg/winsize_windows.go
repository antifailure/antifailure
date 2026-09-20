package termimg

// windowSize reports nothing on Windows.
//
// The console API reports a size in cells and has no pixel equivalent, so there
// is no honest cell size to return. A zero Winsize is read by Interpret as "the
// cell size is unknown", which rules out sixel and leaves the two cell-sized
// protocols, neither of which needs it. Windows Terminal answers the device
// attributes query like any other terminal, so detection itself still works.
func windowSize(uintptr) Winsize { return Winsize{} }
