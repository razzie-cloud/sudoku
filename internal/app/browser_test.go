package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/catalog"
	"github.com/razzie-cloud/sudoku/internal/config"
	"github.com/razzie-cloud/sudoku/internal/store"
	"github.com/razzie-cloud/sudoku/tests/browser"
)

func requireBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("CHROMIUM_EXECUTABLE_PATH") == "" {
		t.Skip("set CHROMIUM_EXECUTABLE_PATH for the production-CSP browser checks")
	}
}

func sudokuBrowser(t *testing.T, storage *store.Store, owner uuid.UUID, collections []catalog.Collection, intercept func(browser.Request) *browser.Response) *browser.Browser {
	t.Helper()
	signer := newTestSigner(t, testOrigin)
	handler, err := New(config.Config{Origin: testOrigin, LandingOrigin: "https://home.example"}, storage, signer.verifier, nil, collections)
	if err != nil {
		t.Fatal(err)
	}
	b := browser.Start(func(req browser.Request) browser.Response {
		if intercept != nil {
			if response := intercept(req); response != nil {
				return *response
			}
		}
		r := httptest.NewRequest(req.Method, req.URL, strings.NewReader(req.PostData))
		for key, value := range req.Headers {
			r.Header.Set(key, value)
		}
		signer.sign(r, testOrigin, owner)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		headers := map[string]string{}
		for key, values := range w.Header() {
			if len(values) > 0 && key != "Content-Type" {
				headers[key] = values[0]
			}
		}
		return browser.Response{Body: w.Body.Bytes(), Status: w.Code, Type: w.Header().Get("Content-Type"), Headers: headers}
	})
	t.Cleanup(b.Close)
	b.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `
window.cspViolations = [];
addEventListener('securitypolicyviolation', event => cspViolations.push(event.violatedDirective));
window.editCell = (index, digit) => {
  const cell = document.querySelector('[data-cell="' + index + '"]');
  cell.click();
  cell.dispatchEvent(new KeyboardEvent('keydown', {key: String(digit), bubbles: true}));
};
`})
	return b
}

func TestBrowserSaveAndNavigation(t *testing.T) {
	requireBrowser(t)
	collections, givens, solution := testPuzzles(t)
	storage := testStorage(t, collections)
	owner := uuid.New()
	var saveStatus atomic.Int32
	var holdNavigation atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	b := sudokuBrowser(t, storage, owner, collections, func(req browser.Request) *browser.Response {
		if req.Method == "GET" && strings.HasSuffix(req.URL, "/puzzles") && holdNavigation.Swap(false) {
			close(started)
			select {
			case <-release:
			case <-time.After(4 * time.Second):
			}
		}
		if req.Method == "POST" && strings.HasSuffix(req.URL, "/save") {
			if status := saveStatus.Load(); status != 0 {
				return &browser.Response{Status: int(status)}
			}
		}
		return nil
	})
	b.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": `
const originalFetch = fetch;
window.saveBodies = [];
const holdFirst = location.pathname === '/puzzles';
window.fetch = async (url, options) => {
  if (!String(url).endsWith('/save')) return originalFetch(url, options);
  saveBodies.push(JSON.parse(options.body));
  if (holdFirst && saveBodies.length === 1) await new Promise(resolve => window.releaseFirstSave = resolve);
  if (window.offline) throw new TypeError('Network unavailable');
  return originalFetch(url, options);
};
`})
	b.Navigate(testOrigin + "/puzzles")
	b.Run(`
window.shell = el('.cloud-header');
el('[data-preview]').click();
await wait(() => location.pathname.endsWith('/1') && document.querySelector('[data-cell][tabindex="0"]'));
assert(el('.cloud-header') === shell, 'shell replaced');
assert(el('[data-cell][tabindex="0"]').dataset.cell === '0', 'first editable cell not keyboard accessible');
el('[data-cell="0"]').focus();
el('[data-cell="0"]').dispatchEvent(new KeyboardEvent('keydown', {key: 'ArrowRight', bubbles: true}));
assert(document.activeElement.dataset.cell === '1', 'arrow key did not move focus');
editCell(0, 9);
await wait(() => window.releaseFirstSave);
el('[data-pencil]').click();
el('[data-cell="1"]').click();
el('[data-key="3"]').click();
assert(el('[data-cell="1"] [data-marks]').children[2].textContent === '3', 'pencil position');
assert(saveBodies.length === 1, 'save was not serialized');
releaseFirstSave();
await wait(() => el('[data-play]').dataset.revision === '2' && el('[data-status]').textContent === 'Saved');
assert(saveBodies[0].revision === 0 && saveBodies[1].revision === 1, 'stale save revision');
assert(el('[data-cell="0"] [data-digit]').textContent === '9', 'first edit lost');
`)
	saved, err := storage.Attempt(t.Context(), owner, "easy-1", 1, givens)
	if err != nil || saved.Revision != 2 || saved.Entries[0] != 9 || saved.Notes[1] != 4 {
		t.Fatalf("saved after serial edits %+v: %v", saved, err)
	}

	b.Run(`
el('[data-back]').click();
await wait(() => location.pathname === '/puzzles' && document.querySelector('.puzzle-grid'));
const tile = el('[data-preview]');
assert(tile.textContent.trim() === '1' && tile.getAttribute('aria-label').includes('In progress'), 'missing progress label or extra tile copy');
await frame();
tile.click();
await wait(() => document.querySelector('[data-play][data-revision="2"] [data-cell][tabindex="0"]'));
assert(el('[data-cell="1"] [data-marks]').children[2].textContent === '3', 'resuming lost marks');
`)

	saveStatus.Store(http.StatusUnauthorized)
	holdNavigation.Store(true)
	b.Run(`el('[data-back]').click();`)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("delayed navigation did not start")
	}
	b.Run(`
editCell(0, 5);
await wait(() => !el('[data-sign-in]').hidden && !el('[data-retry]').hidden);
assert(el('[data-cell="0"] [data-digit]').textContent === '5', 'authentication failure lost draft');
`)
	dialogs := b.DialogCount()
	close(release)
	b.Run(`await new Promise(resolve => setTimeout(resolve, 300)); assert(location.pathname.endsWith('/1') && el('[data-cell="0"] [data-digit]').textContent === '5', 'late navigation dropped draft');`)
	if b.DialogCount() != dialogs+1 {
		t.Fatal("slow navigation did not ask to discard the new draft")
	}
	b.Run(`el('[data-back]').click(); assert(location.pathname.endsWith('/1'), 'dirty HTMX link navigated');`)
	b.Run(`history.back(); await wait(() => location.pathname.endsWith('/1')); await new Promise(resolve => setTimeout(resolve, 300)); assert(location.pathname.endsWith('/1') && el('[data-cell="0"] [data-digit]').textContent === '5', 'history navigation dropped draft');`)
	if b.DialogCount() != dialogs+3 {
		t.Fatalf("dirty link/back guard dialogs=%d, want %d", b.DialogCount(), dialogs+3)
	}
	saveStatus.Store(0)
	b.Run(`
window.offline = true;
el('[data-retry]').click();
await wait(() => el('[data-status]').textContent.includes('Could not save'));
assert(el('[data-cell="0"] [data-digit]').textContent === '5' && !el('[data-retry]').hidden, 'network failure lost draft');
window.offline = false;
el('[data-retry]').click();
await wait(() => el('[data-play]').dataset.revision === '3');
`)
	saved, err = storage.Attempt(t.Context(), owner, "easy-1", 1, givens)
	if err != nil {
		t.Fatal(err)
	}
	other := saved.Edit
	other.Entries[0] = 8
	if _, err := storage.SaveAttempt(t.Context(), owner, "easy-1", 1, givens, other); err != nil {
		t.Fatal(err)
	}
	b.Run(`
editCell(1, 3);
await wait(() => !el('[data-reload]').hidden);
assert(el('[data-cell="1"] [data-digit]').textContent === '3', 'conflict overwrote draft');
setTimeout(() => { window.confirm = () => true; el('[data-reload]').click(); }, 0);
`)
	b.WaitFor(`document.querySelector('[data-play][data-revision="4"] [data-cell][tabindex="0"]')`)
	b.Run(`
assert(el('[data-cell="0"] [data-digit]').textContent === '8', 'reload did not fetch saved board');
editCell(0, 5);
editCell(1, 3);
await wait(() => el('[data-play]').dataset.completed === 'true');
assert(el('[data-status]').textContent === 'Solved' && el('[data-cell="0"]').disabled, 'completion did not lock');
assert(document.querySelector('.fireworks'), 'solved puzzle did not celebrate');
assert(el('[data-next]').textContent === 'Collection complete' && !el('[data-next]').hidden, 'collection completion absent');
`)
	for _, theme := range []string{"light", "dark"} {
		b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": theme}}})
		for _, width := range []int{320, 390, 768, 1440} {
			b.Viewport(width, 1000)
			b.Run(`await frame();fireworks.play();await frame();assert(getComputedStyle(el('.fireworks')).pointerEvents==='none','fireworks block controls');assert(document.documentElement.scrollWidth<=innerWidth,'fireworks overflow');`)
			if width == 390 || width == 1440 {
				b.Run(`await new Promise(resolve=>setTimeout(resolve,700));`)
				captureSudoku(t, b, "fireworks-"+theme+"-"+strconv.Itoa(width))
			}
		}
	}
	b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "reduce"}}})
	b.Run(`await wait(()=>!document.querySelector('.fireworks'));fireworks.play();assert(!document.querySelector('.fireworks'),'reduced motion animated');`)
	b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": "no-preference"}}})
	b.Run(`
fireworks.play();
el('[data-back]').click();
await wait(() => document.querySelector('[data-preview]'));
assert(el('[data-preview]').getAttribute('aria-label').includes('Completed'), 'completion did not persist');
assert(!document.querySelector('.fireworks'), 'fireworks survived navigation');
`)
	b.Run(`
await frame();
el('[data-preview]').click();
await wait(() => document.querySelector('[data-play][data-completed="true"]'));
assert(!document.querySelector('.fireworks'), 'reopened puzzle celebrated again');
assert(cspViolations.length === 0, 'CSP violation: ' + cspViolations);
`)
	saved, err = storage.Attempt(t.Context(), owner, "easy-1", 1, givens)
	if err != nil || saved.Entries != solution || saved.CompletedAt == nil {
		t.Fatalf("solved puzzle %+v: %v", saved, err)
	}
	b.AssertNoErrors()
}

func TestBrowserArchiveAndResume(t *testing.T) {
	requireBrowser(t)
	collections, givens, _ := testPuzzles(t)
	storage := testStorage(t, collections)
	owner, id := uuid.New(), uuid.New()
	if _, err := storage.CreateGame(t.Context(), owner, id, board.Easy, givens); err != nil {
		t.Fatal(err)
	}
	b := sudokuBrowser(t, storage, owner, collections, nil)
	b.Navigate(testOrigin + "/sessions")
	b.Run(`
const card = el('.game-card-link');
assert(card.querySelectorAll('.mini-board span').length === 81, 'missing board thumbnail');
card.click();
await wait(() => document.querySelector('[data-cell][tabindex="0"]'));
editCell(0, 5);
el('[data-archive] button').click();
`)
	b.WaitFor(`location.pathname === '/archive' && document.querySelector('.game-card-link') && document.readyState === 'complete'`)
	b.Run(`
assert(el('.game-card-link .mini-board').firstElementChild.textContent === '5', 'archiving lost latest edit');
el('.game-card-link').click();
await wait(() => document.querySelector('[data-play][data-readonly="true"]'));
await frame();
setTimeout(() => el('[data-archive] button').click(), 0);
`)
	b.WaitFor(`document.querySelector('[data-play][data-readonly="false"] [data-cell][tabindex="0"]')`)
	b.Run(`
assert(el('[data-cell="0"] [data-digit]').textContent === '5', 'resume lost saved digit');
editCell(1, 3);
await wait(() => document.querySelector('[data-play][data-completed="true"]'));
assert(el('.app-nav [aria-current="page"]').textContent === 'Archive' && el('[data-back]').getAttribute('href') === '/archive', 'archive navigation not active after solve');
assert(document.querySelector('.fireworks'), 'solved random game did not celebrate');
el('[data-back]').click();
await wait(() => location.pathname === '/archive' && document.querySelector('.game-card-link'));
assert(el('.game-card-link .mini-board').children[1].textContent === '3', 'archived solved thumbnail stale');
assert(el('.game-card-link').textContent.includes('Solved'), 'archived game status absent');
await frame();
el('.app-nav a[href="/games/new"]').click();
await wait(() => document.querySelector('#difficulty[data-ready="true"]'));
const difficulty = el('#difficulty');
assert(difficulty.options.length === 5, 'missing difficulties');
const original = el('[data-example]').getAttribute('aria-label');
difficulty.value = 'master';
difficulty.dispatchEvent(new Event('change'));
await wait(() => el('[data-example]').getAttribute('aria-label') !== original);
setTimeout(() => el('[data-setup] button').click(), 0);
`)
	b.WaitFor(`location.pathname.startsWith('/games/') && document.querySelector('h1')?.textContent === 'Master game' && document.querySelector('[data-cell][tabindex="0"]')`)
	b.AssertNoErrors()
}

func TestBrowserLayoutsAndPreviews(t *testing.T) {
	requireBrowser(t)
	collections, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	storage := testStorage(t, collections)
	owner := uuid.New()
	easy := collections[0].Puzzles[0]
	edit := store.Edit{Entries: easy}
	for i, value := range easy {
		if value == 0 {
			edit.Notes[i] = 1
			break
		}
	}
	if _, err := storage.SaveAttempt(t.Context(), owner, "easy-1", 1, easy, edit); err != nil {
		t.Fatal(err)
	}
	b := sudokuBrowser(t, storage, owner, collections, nil)
	for _, theme := range []string{"light", "dark"} {
		b.Call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": theme}}})
		for _, width := range []int{320, 390, 768, 1440} {
			t.Run(theme+"/"+strconv.Itoa(width), func(t *testing.T) {
				b.Viewport(width, 1000)
				b.Navigate(testOrigin + "/puzzles")
				b.Call("Page.bringToFront", nil)
				b.Run(`
assert(document.querySelectorAll('[data-preview]').length === 160, 'collection catalog missing');
const first = el('[data-preview]');
const second = document.querySelectorAll('[data-preview]')[1];
assert(getComputedStyle(first).borderColor !== getComputedStyle(second).borderColor, 'in-progress border color absent');
assert(Math.abs(first.offsetHeight - second.offsetHeight) < 1 && first.textContent.trim() === '1', 'progress changed tile dimensions');
second.focus({preventScroll:true});
await wait(() => !el('#puzzle-tooltip').hidden && el('#puzzle-tooltip .mini-board').childElementCount === 81);
const tip = el('#puzzle-tooltip').getBoundingClientRect();
assert(tip.left >= 0 && tip.right <= innerWidth && tip.top >= 0 && tip.bottom <= innerHeight, 'tooltip outside viewport');
assert(second.getAttribute('aria-describedby') === 'puzzle-tooltip', 'keyboard tooltip lacks association');
assert(document.documentElement.scrollWidth <= innerWidth, 'collection horizontally overflowed');
assert(cspViolations.length === 0, 'CSP violation: ' + cspViolations);
`)
				if width == 390 || width == 1440 {
					captureSudoku(t, b, "collections-"+theme+"-"+strconv.Itoa(width))
				}
				if width == 390 {
					b.Run(`
const second = document.querySelectorAll('[data-preview]')[1];
second.blur();
second.dispatchEvent(new PointerEvent('pointerdown', {pointerType: 'touch', pointerId: 1, bubbles: true}));
await wait(() => !el('#puzzle-tooltip').hidden);
second.dispatchEvent(new PointerEvent('pointerup', {pointerType: 'touch', pointerId: 1, bubbles: true}));
second.click();
assert(location.pathname === '/puzzles', 'long-press preview navigated');
`)
				}
				b.Run(`
const second = document.querySelectorAll('[data-preview]')[1];
document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
assert(el('#puzzle-tooltip').hidden, 'escape did not hide tooltip');
second.blur();
second.dispatchEvent(new PointerEvent('pointerover', {pointerType: 'mouse', bubbles: true}));
assert(!el('#puzzle-tooltip').hidden, 'hover did not show tooltip');
second.click();
await wait(() => location.pathname.endsWith('/2') && document.querySelector('[data-cell][tabindex="0"]'));
assert(document.documentElement.scrollWidth <= innerWidth, 'play layout horizontally overflowed');
const board = el('.play-board .sudoku-grid').getBoundingClientRect();
assert(Math.abs(board.width - board.height) < 2, 'board is not square');
const first = el('[data-cell="0"]').getBoundingClientRect();
const secondCell = el('[data-cell="1"]').getBoundingClientRect();
assert(Math.abs(secondCell.left - first.right) < .4, 'cells overlap their grid lines');
assert(el('.app-nav [aria-current="page"]').textContent === 'Puzzles', 'wrong active nav');
`)
				if width == 390 || width == 1440 {
					captureSudoku(t, b, "play-"+theme+"-"+strconv.Itoa(width))
				}
				b.Eval("setTimeout(() => history.back(), 0)")
				b.WaitFor(`location.pathname === '/puzzles' && document.querySelector('[data-preview]') && document.readyState === 'complete'`)
				b.Eval("setTimeout(() => history.forward(), 0)")
				b.WaitFor(`location.pathname.endsWith('/2') && document.querySelector('[data-cell][tabindex="0"]')`)
				b.Run(`assert(cspViolations.length === 0, 'CSP violation: ' + cspViolations);`)
			})
		}
	}
	b.AssertNoErrors()
}

func captureSudoku(t *testing.T, b *browser.Browser, name string) {
	t.Helper()
	dir := os.Getenv("SUDOKU_SCREENSHOT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	var screenshot struct{ Data string }
	if err := json.Unmarshal(b.Call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}), &screenshot); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(screenshot.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0644); err != nil {
		t.Fatal(err)
	}
}
