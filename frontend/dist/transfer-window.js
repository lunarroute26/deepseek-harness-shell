(() => {
  'use strict';

  const tasksElement = document.getElementById('tasks');
  const summaryElement = document.getElementById('summary');
  const clearButton = document.getElementById('clear');
  let snapshot = { tasks: [] };

  function formatBytes(value) {
    const bytes = Number(value) || 0;
    if (bytes < 1024) return `${bytes} B`;
    const units = ['KB', 'MB', 'GB', 'TB'];
    let amount = bytes;
    let unit = 'B';
    for (const next of units) {
      amount /= 1024;
      unit = next;
      if (amount < 1024) break;
    }
    return `${amount.toFixed(amount >= 100 ? 0 : amount >= 10 ? 1 : 2)} ${unit}`;
  }

  function statusText(status) {
    return {
      downloading: '正在下载',
      complete: '已完成',
      error: '下载失败',
      cancelled: '已取消',
    }[status] || status;
  }

  function send(message) {
    const invoke = window._wails?.invoke;
    if (typeof invoke !== 'function') return false;
    invoke(JSON.stringify({ version: 1, ...message }));
    return true;
  }

  function actionButton(label, action, taskId, className = '') {
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = label;
    button.dataset.action = action;
    button.dataset.taskId = taskId;
    if (className) button.className = className;
    return button;
  }

  function renderTask(task) {
    const article = document.createElement('article');
    article.className = 'task';

    const head = document.createElement('div');
    head.className = 'task-head';
    const filename = document.createElement('div');
    filename.className = 'filename';
    filename.textContent = task.filename;
    filename.title = task.destination || task.filename;
    const status = document.createElement('div');
    status.className = `status ${task.status}`;
    status.textContent = statusText(task.status);
    head.append(filename, status);
    article.append(head);

    if (task.status === 'downloading') {
      const track = document.createElement('div');
      track.className = `progress-track${task.totalBytes > 0 ? '' : ' indeterminate'}`;
      const bar = document.createElement('div');
      bar.className = 'progress-bar';
      if (task.totalBytes > 0) {
        bar.style.width = `${Math.min(100, task.bytesWritten / task.totalBytes * 100)}%`;
      }
      track.append(bar);
      article.append(track);
    }

    const meta = document.createElement('div');
    meta.className = 'task-meta';
    const size = document.createElement('span');
    size.textContent = task.totalBytes > 0
      ? `${formatBytes(task.bytesWritten)} / ${formatBytes(task.totalBytes)}`
      : formatBytes(task.bytesWritten);
    const speed = document.createElement('span');
    speed.textContent = task.status === 'downloading' && task.bytesPerSecond > 0
      ? `${formatBytes(task.bytesPerSecond)}/s`
      : '';
    meta.append(size, speed);
    article.append(meta);

    if (task.error) {
      const error = document.createElement('p');
      error.className = 'error-message';
      error.textContent = task.error;
      article.append(error);
    }

    const actions = document.createElement('div');
    actions.className = 'task-actions';
    if (task.status === 'downloading') {
      actions.append(actionButton('取消下载', 'cancel', task.id, 'danger'));
    } else if (task.status === 'complete') {
      actions.append(
        actionButton('打开文件', 'open', task.id),
        actionButton('打开所在文件夹', 'reveal', task.id),
      );
    }
    if (actions.childElementCount > 0) article.append(actions);
    return article;
  }

  function render() {
    const tasks = Array.isArray(snapshot.tasks) ? snapshot.tasks : [];
    tasksElement.replaceChildren(...tasks.map(renderTask));
    if (tasks.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'empty';
      empty.textContent = '暂无下载任务';
      tasksElement.append(empty);
    }

    const active = tasks.filter((task) => task.status === 'downloading').length;
    summaryElement.textContent = active > 0 ? `${active} 个任务正在下载` : `${tasks.length} 个下载任务`;
    clearButton.hidden = !tasks.some((task) => task.status !== 'downloading');
  }

  tasksElement.addEventListener('click', (event) => {
    const button = event.target.closest('button[data-action]');
    if (!button) return;
    send({
      type: 'dsh-shell:download-action',
      action: button.dataset.action,
      taskId: button.dataset.taskId,
    });
  });

  clearButton.addEventListener('click', () => {
    send({ type: 'dsh-shell:download-action', action: 'clear', taskId: '' });
  });

  window.__dshShellDownloadUpdate = (nextSnapshot) => {
    snapshot = nextSnapshot && typeof nextSnapshot === 'object' ? nextSnapshot : { tasks: [] };
    render();
  };

  function announceReady() {
    if (send({ type: 'dsh-shell:download-window-ready' })) return;
    setTimeout(announceReady, 50);
  }

  render();
  announceReady();
})();
