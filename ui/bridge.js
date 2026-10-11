'use strict';

// Wails injects the native IPC bridge before loading the page. Keeping these
// wrappers preserves the shared interface and does not require a JS build tool.
for (const [name, method] of Object.entries({
  getState: 'GetState',
  checkConnection: 'CheckConnection',
  startBackup: 'StartBackup',
  cancelBackup: 'CancelBackup',
  chooseDirectory: 'ChooseDirectory',
  openArchiveFolder: 'OpenArchiveFolder',
  getEnvironment: 'GetEnvironment',
  getSavedSettings: 'GetSavedSettings',
  saveLastSettings: 'SaveLastSettings',
  saveConnection: 'SaveConnection',
  loadConnection: 'LoadConnection',
  deleteConnection: 'DeleteConnection',
  frontendReady: 'FrontendReady',
  closeAfterSaving: 'CloseAfterSaving',
})) {
  window[name] = (...args) => {
    const app = window.go?.main?.App;
    if (!app || typeof app[method] !== 'function') {
      return Promise.reject(new Error('Связь с приложением ещё не готова. Перезапустите окно.'));
    }
    return app[method](...args);
  };
}
