import { app, BrowserWindow, ipcMain } from "electron";
import path from "path";

const DEV_SERVER_URL = "http://localhost:5173";

const createWindow = () => {
  const win = new BrowserWindow({
    width: 360,
    height: 520,
    resizable: false,
    transparent: true,
    frame: false,
    alwaysOnTop: true,
    skipTaskbar: true,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
    },
  });

  win.loadURL(DEV_SERVER_URL);
};

app.whenReady().then(() => {
  createWindow();

  ipcMain.handle("window-move", (event, { x, y }) => {
    const win = BrowserWindow.fromWebContents(event.sender);
    if (win) {
      win.setPosition(Math.round(x), Math.round(y));
    }
  });

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
