import { app, BrowserWindow, ipcMain, Menu } from "electron";
import path from "path";

const DEV_SERVER_URL = "http://localhost:5173";

let settingsWindow: BrowserWindow | null = null;

const createWindow = () => {
  const win = new BrowserWindow({
    width: 360,
    height: 520,
    resizable: false,
    transparent: true,
    frame: false,
    alwaysOnTop: true,
    skipTaskbar: true,
    hasShadow: false,
    backgroundColor: "#00000000",
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
    },
  });

  win.loadURL(DEV_SERVER_URL);

  attachContextMenu(win);
  return win;
};

const createSettingsWindow = () => {
  if (settingsWindow) {
    settingsWindow.focus();
    return;
  }
  settingsWindow = new BrowserWindow({
    width: 420,
    height: 560,
    resizable: true,
    transparent: false,
    frame: true,
    backgroundColor: "#f3f4f6",
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
    },
  });

  settingsWindow.loadURL(`${DEV_SERVER_URL}/?settings=1`);
  settingsWindow.on("closed", () => {
    settingsWindow = null;
  });

  attachContextMenu(settingsWindow);
};

const attachContextMenu = (win: BrowserWindow) => {
  win.webContents.on("context-menu", () => {
    const menu = Menu.buildFromTemplate([
      {
        label: "设置",
        click: () => createSettingsWindow(),
      },
      {
        label: "退出",
        click: () => app.quit(),
      },
    ]);
    menu.popup({ window: win });
  });
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
