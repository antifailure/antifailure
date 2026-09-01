# fixed

Following a link into the console while signed out lost the page you were going
to. The sign-in screen sent you to the dashboard, so every deep link this
product publishes, including the environment link in a pull request comment, was
a link to the front door. The device approval page had always done this
correctly and the rest of the console had not.

Both ways in now carry the return path, query string included:
`/environments?env=af-1234` and `/environments` are different pages to somebody
who followed a link to one of them.
