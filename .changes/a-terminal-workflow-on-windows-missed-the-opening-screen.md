# fixed

On Windows, a terminal workflow could miss the screen a program opened on.

Windows writes a few setup bytes to a program's terminal before the program has
drawn anything, and none of them puts a character on the screen. Antifailure
took them for the program's first screen, read a blank grid, and sent the first
key while the program was still starting. The program then drew its opening
screen and answered the key at once, so the report began on the screen after the
first key, as if that key had been pressed on a screen nobody saw. Before the
first key, Antifailure now waits until something is on the screen, up to the
same three seconds it already gave a program that printed nothing.
