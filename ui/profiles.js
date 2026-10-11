'use strict';

window.profileUI = (() => {
  const el = id => document.getElementById(id);
  let ready = false, timer, currentAlias = '', aliases = [], lastSignature = '';
  let queue = Promise.resolve();

  function enqueue(fn) {
    const result = queue.then(fn);
    queue = result.catch(() => {});
    return result;
  }

  function status(text, warning = false) {
    el('profileStatus').textContent = text;
    el('profileStatus').classList.toggle('warning', warning);
  }

  function refresh(list, selected = currentAlias) {
    aliases = list || [];
    const select = el('profileSelect');
    select.replaceChildren(new Option('Без профиля · последние параметры', ''));
    for (const name of aliases) select.add(new Option(name, name));
    currentAlias = aliases.includes(selected) ? selected : '';
    select.value = currentAlias;
    updateControls(busy || requesting);
  }

  function updateControls(locked) {
    for (const id of ['profileSelect', 'profileName', 'saveProfile']) el(id).disabled = locked || !ready;
    el('deleteProfile').disabled = locked || !ready || !currentAlias;
  }

  function apply(c) {
    for (const id of fields) {
      if (id === 'compress') el(id).checked = !!c[id];
      else el(id).value = c[id] ?? (id === 'parallel' ? 4 : '');
    }
    el('secretKey').type = 'password';
    el('toggleSecret').setAttribute('aria-label', 'Показать секретный ключ');
  }

  function persist() {
    clearTimeout(timer);
    if (!ready) return Promise.resolve();
    const c = config(), alias = currentAlias, signature = JSON.stringify([c, alias]);
    return enqueue(async () => {
      if (signature === lastSignature) return;
      status('Сохраняем последние параметры…');
      try {
        const result = await window.saveLastSettings(c, alias);
        lastSignature = signature;
        status(result.warning || 'Последние параметры сохранены', !!result.warning);
      } catch (err) {
        status('Не удалось запомнить параметры: ' + String(err), true);
        throw err;
      }
    });
  }

  function schedule() {
    if (!ready) return;
    clearTimeout(timer);
    status('Изменения сохранятся автоматически');
    timer = setTimeout(() => persist().catch(() => {}), 600);
  }

  async function init() {
    // Wait for the latest form values before destroying the native webview.
    if (window.runtime?.EventsOn) {
      window.runtime.EventsOn('save-before-close', () => {
        persist().catch(() => {}).finally(() => window.closeAfterSaving());
      });
    }
    for (const id of fields) {
      el(id).addEventListener('input', schedule);
      el(id).addEventListener('change', schedule);
      el(id).addEventListener('blur', () => { if (timer) persist().catch(() => {}); });
    }
    el('profileSelect').onchange = () => {
      const name = el('profileSelect').value;
      action(async () => {
        await persist().catch(() => {});
        if (!name) {
          currentAlias = '';
          el('profileName').value = '';
          await persist();
          return;
        }
        const loaded = await enqueue(() => window.loadConnection(name));
        apply(loaded.config);
        currentAlias = loaded.name;
        el('profileName').value = loaded.name;
        refresh(aliases, currentAlias);
        await persist().catch(() => {});
        status(loaded.warning || 'Загружено подключение «' + loaded.name + '»', !!loaded.warning);
      }).finally(() => { el('profileSelect').value = currentAlias; });
    };
    el('saveProfile').onclick = () => action(async () => {
      clearTimeout(timer);
      const name = el('profileName').value, c = config();
      const saved = await enqueue(() => window.saveConnection(name, c));
      currentAlias = saved.name;
      el('profileName').value = saved.name;
      refresh(saved.aliases, saved.name);
      lastSignature = JSON.stringify([c, saved.name]);
      status(saved.warning || 'Подключение «' + saved.name + '» сохранено', !!saved.warning);
    });
    el('deleteProfile').onclick = () => action(async () => {
      clearTimeout(timer);
      const name = currentAlias;
      const result = await enqueue(() => window.deleteConnection(name));
      currentAlias = '';
      el('profileName').value = '';
      refresh(result.aliases, '');
      await persist();
      status('Профиль «' + name + '» удалён. Текущие параметры оставлены в форме.');
    });
    try {
      const saved = await window.getSavedSettings();
      if (saved.last) apply(saved.last);
      ready = true;
      refresh(saved.aliases, saved.lastAlias);
      el('profileName').value = currentAlias;
      lastSignature = JSON.stringify([config(), currentAlias]);
      status(saved.warning || (saved.last ? 'Последние параметры восстановлены' : 'Параметры будут сохраняться автоматически'), !!saved.warning);
    } catch (err) {
      status('Сохранённые подключения недоступны: ' + String(err), true);
    } finally {
      updateControls(busy || requesting);
      await window.frontendReady();
    }
  }

  return {init, persist, schedule, controls: updateControls};
})();
