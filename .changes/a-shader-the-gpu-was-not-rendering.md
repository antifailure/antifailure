# fixed

The hero shader ran on the CPU on any machine whose GPU driver is blocklisted.

The component guarded against WebGL being unavailable, and that guard is
binary: the context is refused, or it works. A blocklisted driver is neither.
The browser grants a context backed by a software rasteriser instead of
refusing one, so `new Renderer` succeeds, nothing throws, and a full bleed
fragment shader animates on the CPU for as long as the page is open. That is
the normal state of a cheap or out of support Chromebook, and it was reported
as the site crashing them.

`failIfMajorPerformanceCaveat` is the context attribute that turns that third
case back into the refusal the component already handles well, and ogl builds
its own attribute object without forwarding it, so the question is now asked
separately before the real context is built. A machine with a working GPU
answers yes and loses nothing.

The loop also never stopped. Frame throttling applies to a hidden tab, not to
an element that has scrolled out of a visible one, so a reader who scrolled
past the hero kept paying for the shader for the rest of the visit. It now runs
only while the canvas is on screen in a tab somebody is looking at.
