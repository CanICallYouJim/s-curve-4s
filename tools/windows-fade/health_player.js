const tracks = [...document.querySelectorAll('.track')];
const search = document.getElementById('search');
const filter = document.getElementById('filter');

function applyFilter() {
  let count = 0;
  const query = search.value.toLocaleLowerCase();
  for (const track of tracks) {
    const status = track.dataset.status;
    const matches = filter.value === 'all' ||
      (filter.value === 'problems' ? status !== 'clean' : status === filter.value);
    track.hidden = !(matches && track.dataset.path.toLocaleLowerCase().includes(query));
    if (!track.hidden) count++;
  }
  const empty = document.getElementById('empty');
  empty.hidden = count !== 0;
  empty.textContent = !query && filter.value === 'problems'
    ? 'Проверка не нашла записей, которые нужно прослушать из-за возможной проблемы со звуком.'
    : 'По этому запросу ничего не найдено.';
}
search.addEventListener('input', applyFilter);
filter.addEventListener('change', applyFilter);
applyFilter();

const player = document.getElementById('player');
const playing = document.getElementById('playing');
const picker = document.getElementById('musicFiles');
const chooseMusic = document.getElementById('chooseMusic');
const accessStatus = document.getElementById('accessStatus');
let selectedFiles = new Map();
let pendingPlay = null;
let activeFile = null;
let activeURL = '';
let startAt = 0;
let activePreview = '';

function audioPath(path) {
  return path.replace(/\\/g, '/').normalize('NFC');
}

function resetPlayer() {
  player.pause();
  player.removeAttribute('src');
  player.load();
  if (activeURL) URL.revokeObjectURL(activeURL);
  activeURL = '';
  activeFile = null;
  activePreview = '';
}

function chooseFolder() {
  picker.value = '';
  picker.click();
}

function seekAndPlay() {
  if (!activeURL || player.currentSrc !== activeURL) return;
  const duration = player.duration;
  player.currentTime = Number.isFinite(duration)
    ? Math.min(startAt, Math.max(0, duration - .01)) : startAt;
  if (player.paused) requestPlayback();
}

function requestPlayback() {
  const requestedURL = activeURL;
  player.play().catch(() => {
    if (activeURL === requestedURL) {
      playing.textContent = 'Нажмите кнопку воспроизведения в плеере.';
    }
  });
}

function playTrack(path, start, label) {
  const file = selectedFiles.get(audioPath(path));
  if (!file) {
    if (selectedFiles.size === 0) {
      pendingPlay = {path, start, label};
      accessStatus.textContent = 'Выберите корневую музыкальную папку, указанную в начале отчёта.';
      chooseFolder();
    } else {
      playing.textContent = 'В выбранной папке не найден файл «' + path +
        '». Выберите корневую музыкальную папку из отчёта ещё раз.';
    }
    return;
  }
  startAt = Number.isFinite(start) && start > 0 ? start : 0;
  playing.textContent = path + (label ? ' · ' + label : '');
  if (activeFile === file && player.readyState >= 1) {
    seekAndPlay();
    return;
  }
  loadAudio(file);
}

function loadAudio(file) {
  player.pause();
  const previousURL = activeURL;
  activeFile = file;
  activePreview = '';
  activeURL = URL.createObjectURL(file);
  player.src = activeURL;
  player.load();
  if (previousURL) URL.revokeObjectURL(previousURL);
  // Start inside the trusted click, then seek when metadata becomes available.
  requestPlayback();
}

function playPreview(button) {
  const previewID = button.dataset.previewId;
  const origin = Number(button.dataset.previewStart);
  const absoluteStart = Number(button.dataset.start);
  startAt = Math.max(0, absoluteStart - origin);
  playing.textContent = button.dataset.audioPath + ' · фрагмент ' + button.dataset.previewRange;
  if (activePreview === previewID && player.readyState >= 1) {
    seekAndPlay();
    return;
  }
  try {
    const encoded = document.getElementById(previewID).textContent.trim();
    const bytes = Uint8Array.from(atob(encoded), character => character.charCodeAt(0));
    loadAudio(new Blob([bytes], {type: 'audio/mpeg'}));
    activePreview = previewID;
  } catch (_) {
    playing.textContent = 'Не удалось прочитать встроенный фрагмент. Для прослушивания исходника выберите папку с музыкой.';
  }
}

chooseMusic.addEventListener('click', () => {
  pendingPlay = null;
  chooseFolder();
});

picker.addEventListener('change', () => {
  if (picker.files.length === 0) return;
  resetPlayer();
  const files = new Map();
  const wanted = new Set(tracks.map(track => audioPath(track.dataset.path)));
  for (const file of picker.files) {
    const relative = audioPath(file.webkitRelativePath || file.name);
    const separator = relative.indexOf('/');
    const path = separator >= 0 ? relative.slice(separator + 1) : relative;
    if (!wanted.has(path)) continue;
    // Never silently choose between two distinct files with the same key.
    files.set(path, files.has(path) ? null : file);
  }
  selectedFiles = files;
  let available = 0;
  for (const track of tracks) {
    if (files.get(audioPath(track.dataset.path))) available++;
  }
  accessStatus.textContent = 'Доступно записей из отчёта: ' + available + ' из ' + tracks.length +
    '. Файлы используются локально в этой странице.';
  if (available === 0) {
    accessStatus.textContent += ' Выберите корневую музыкальную папку, указанную в начале отчёта.';
  }
  const request = pendingPlay;
  pendingPlay = null;
  if (request) playTrack(request.path, request.start, request.label);
});

picker.addEventListener('cancel', () => { pendingPlay = null; });
player.addEventListener('loadedmetadata', seekAndPlay);
player.addEventListener('error', () => {
  if (activeFile) {
    playing.textContent = 'Браузер не смог воспроизвести выбранный файл. Этот формат можно прослушать во внешнем плеере. При изменении файла выберите папку заново.';
  }
});
for (const button of document.querySelectorAll('button[data-audio-path]')) {
  button.addEventListener('click', () => {
    if (button.dataset.previewId && !selectedFiles.get(audioPath(button.dataset.audioPath))) {
      playPreview(button);
    } else {
      playTrack(button.dataset.audioPath, Number(button.dataset.start), button.textContent);
    }
  });
}
window.addEventListener('pagehide', resetPlayer);
