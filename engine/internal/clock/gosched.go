package clock

import "runtime"

func runtimeGosched() { runtime.Gosched() }
