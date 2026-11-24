const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

class Element {
  constructor(dataset = {}) {
    this.dataset = dataset;
    this.handlers = new Map();
    this.textContent = '';
    this.value = '';
    this.hidden = false;
  }
  addEventListener(name, fn) {
    const list = this.handlers.get(name) || [];
    list.push(fn);
    this.handlers.set(name, list);
  }
  fire(name) { for (const fn of this.handlers.get(name) || []) fn(); }
}

const tracks = [
  new Element({path: '_дубли\\Kuoga., Artemisia - Quiet.mp3', status: 'warning'}),
  new Element({path: 'Другая/Kuoga., Artemisia - Quiet.mp3', status: 'clean'}),
  new Element({path: 'Папка/Кай.mp3', status: 'warning'}),
];
const full = new Element({audioPath: tracks[0].dataset.path, start: '0'});
const timed = new Element({audioPath: tracks[0].dataset.path, start: '74.5', previewId: 'preview-0', previewStart: '71', previewRange: '1:11.00–1:20.00'});
const other = new Element({audioPath: tracks[1].dataset.path, start: '1'});
const unicode = new Element({audioPath: tracks[2].dataset.path, start: '2'});
full.textContent = 'Слушать целиком'; timed.textContent = '1:16.00';
const buttons = [full, timed, other, unicode];
const elements = Object.fromEntries(['search', 'filter', 'empty', 'player', 'playing', 'musicFiles', 'chooseMusic', 'accessStatus', 'preview-0'].map(name => [name, new Element()]));
elements.filter.value = 'problems';
elements['preview-0'].textContent = Buffer.from('embedded preview data').toString('base64');
const player = elements.player;
player.readyState = 0;
player.duration = 9;
player.playCount = 0;
player.failPlay = false;
player.paused = true;
player.pause = () => { player.paused = true; };
player.load = () => { player.readyState = 0; player.currentSrc = player.src || ''; };
player.removeAttribute = name => { if (name === 'src') player.src = ''; };
player.play = () => { player.playCount++; if (!player.failPlay) player.paused = false; return player.failPlay ? Promise.reject(new Error('autoplay denied')) : Promise.resolve(); };
const picker = elements.musicFiles;
picker.files = [];
picker.clickCount = 0;
picker.click = () => { picker.clickCount++; };
const created = [], revoked = [];
const window = new Element();
vm.runInNewContext(fs.readFileSync(__dirname + '/health_player.js', 'utf8'), {
  document: {
    getElementById: name => elements[name],
    querySelectorAll: selector => selector === '.track' ? tracks : buttons,
  },
  window, Blob, atob, console,
  URL: {
    createObjectURL(file) { created.push(file); return 'blob:local-' + created.length; },
    revokeObjectURL(url) { revoked.push(url); },
  },
});
function metadata(duration) { player.duration = duration; player.readyState = 1; player.fire('loadedmetadata'); }
function select(files) { picker.files = files; picker.fire('change'); }

(async () => {
  assert.equal(tracks[1].hidden, true);
  // A built-in excerpt must work without opening any local file URL or picker.
  timed.fire('click');
  assert.equal(picker.clickCount, 0);
  assert.match(player.src, /^blob:/);
  assert.equal(created[0].type, 'audio/mpeg');
  assert.equal(Buffer.from(await created[0].arrayBuffer()).toString(), 'embedded preview data');
  assert.equal(player.playCount, 1, "play must start in the click before loadedmetadata");
  metadata(9);
  assert.equal(player.currentTime, 3.5);
  assert.equal(player.playCount, 1);
  assert.match(elements.playing.textContent, /фрагмент/);

  full.fire('click');
  assert.equal(picker.clickCount, 1);
  const first = {name: 'Kuoga., Artemisia - Quiet.mp3', webkitRelativePath: 'Музыка/_дубли/Kuoga., Artemisia - Quiet.mp3'};
  const second = {name: first.name, webkitRelativePath: 'Музыка/Другая/' + first.name};
  const nfd = {name: 'Кай.mp3'.normalize('NFD'), webkitRelativePath: 'Музыка/Папка/Кай.mp3'.normalize('NFD')};
  select([first, second, nfd]);
  assert.equal(created.at(-1), first);
  metadata(150);
  assert.equal(player.currentTime, 0);
  assert.match(elements.accessStatus.textContent, /3 из 3/);
  assert(revoked.includes('blob:local-1'));

  // A selected full source is preferred over the embedded excerpt.
  timed.fire('click');
  assert.equal(created.at(-1), first);
  assert.equal(player.currentTime, 74.5);
  other.fire('click');
  metadata(100);
  assert.equal(created.at(-1), second);
  assert.equal(player.currentTime, 1);
  unicode.fire('click');
  metadata(50);
  assert.equal(created.at(-1), nfd);
  assert.equal(player.currentTime, 2);

  // The last request wins during rapid changes before metadata arrives.
  other.fire('click');
  full.fire('click');
  metadata(150);
  assert.equal(created.at(-1), first);
  assert.equal(player.currentTime, 0);
  assert(revoked.length >= 3);

  player.failPlay = true;
  player.pause();
  timed.fire('click');
  await new Promise(resolve => setImmediate(resolve));
  assert.match(elements.playing.textContent, /Нажмите кнопку воспроизведения/);
  player.failPlay = false;

  elements.chooseMusic.fire('click');
  select([second]);
  full.fire('click');
  assert.match(elements.playing.textContent, /не найден файл/);
  assert(!String(player.src).startsWith('file:'));
  window.fire('pagehide');
  assert.equal(player.src, '');
  console.log('PASS: automatic embedded preview, folder access, Unicode, duplicate names, timing, URL cleanup, visible play rejection.');
})().catch(error => { console.error(error); process.exitCode = 1; });
