// Journal: an Electron client with a loading state, which is the whole reason
// it exists.
//
// runner/test/desktop.test.ts drives it to prove the desktop surface judges an
// application on what it shows once it has loaded, not on its loading screen.
// The shape is copied from the client that exposed the failure: the journal is
// fetched HERE, in the main process, from AF_BASE_URL, and handed to the page
// over one IPC channel. So nothing the page's own network does can tell a
// reader the page is still loading, which is exactly what made the failure
// possible. Until the answer lands the page says "Reading the ledger".

const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('node:path');

const base = (process.env.AF_BASE_URL || '').replace(/\/+$/, '');

ipcMain.handle('journal', async () => {
  if (!base) return { state: 'unconfigured' };
  try {
    const res = await fetch(base + '/journal', { signal: AbortSignal.timeout(10_000) });
    if (!res.ok) return { state: 'refused', status: res.status };
    const body = await res.json();
    if (!Array.isArray(body)) return { state: 'refused', status: res.status };
    // One row at a time, so a row of an unexpected shape costs that row and
    // not the journal.
    const rows = body.filter((r) => r && Number.isInteger(r.seq) && typeof r.event === 'string');
    return { state: 'ok', rows };
  } catch (err) {
    return { state: 'unreachable', detail: String(err && err.message ? err.message : err) };
  }
});

app.whenReady().then(() => {
  const window = new BrowserWindow({
    width: 720,
    height: 520,
    title: 'Journal',
    show: false,
    backgroundColor: '#F6F3EC',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  });
  window.loadFile(path.join(__dirname, 'index.html'));
  window.once('ready-to-show', () => window.show());
});
app.on('window-all-closed', () => app.quit());
