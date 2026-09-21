// Ledger: the Electron application the desktop surface is proven against.
//
// It is a FIXTURE and it is also the application a person sees being driven,
// so it has two jobs. Being driveable is the reason it exists: every control
// carries a real accessible name, the acknowledgment is really `required`, and
// the driver finds all of them through the accessibility tree rather than
// through anything this file could rename. Being worth looking at is the other
// one, and the two are the same work, because an application built so that a
// screen reader can use it is an application built so that an agent can.
//
// titleBarStyle hiddenInset because that is how a macOS application of this
// shape is built: the traffic lights sit in the window's own content rather
// than in a bar above it, and the top of the layout leaves room for them.

const { app, BrowserWindow } = require('electron');
const path = require('node:path');

function open() {
  const window = new BrowserWindow({
    width: 860,
    height: 600,
    // Not resizable below the point the two column layout stops working. A
    // window that can be dragged into a broken shape is a defect somebody
    // will screenshot.
    minWidth: 720,
    minHeight: 520,
    title: 'Ledger',
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    // The window is painted before it is shown, so it never appears as a
    // white rectangle that then fills in.
    show: false,
    backgroundColor: '#FBFBFD',
  });
  window.loadFile(path.join(__dirname, 'index.html'));
  window.once('ready-to-show', () => window.show());
}

app.whenReady().then(open);
app.on('window-all-closed', () => app.quit());
app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) open();
});
