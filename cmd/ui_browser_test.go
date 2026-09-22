package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

var uiBrowserURL = flag.String("ui-browser-url", "", "isolated UI visual fixture URL for browser checks")
var uiScreens = flag.String("ui-screens", "", "optional directory for browser screenshots")
var uiChrome = flag.String("ui-chrome", "", "Chromium executable for optional browser checks")

// TestUIBrowserWorkflows runs only against the isolated fixture. External tabs
// are stubbed: no LinkedIn forms, Gmail API or collector endpoints are contacted.
func TestUIBrowserWorkflows(t *testing.T) {
	if *uiBrowserURL == "" {
		t.Skip("pass -args -ui-browser-url=http://127.0.0.1:18081")
	}
	chrome := *uiChrome
	if chrome == "" {
		home, _ := os.UserHomeDir()
		candidates, _ := filepath.Glob(filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux64/chrome"))
		if len(candidates) > 0 {
			chrome = candidates[len(candidates)-1]
		}
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox)
	if chrome != "" {
		opts = append(opts, chromedp.ExecPath(chrome))
	}
	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if e, ok := ev.(*runtime.EventExceptionThrown); ok {
			t.Errorf("browser exception: %v", e.ExceptionDetails)
		}
	})
	run := func(actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(ctx, actions...); err != nil {
			t.Fatal(err)
		}
	}
	eval := func(js string) { t.Helper(); run(chromedp.Evaluate(js, nil)) }
	check := func(js string) {
		t.Helper()
		var ok bool
		run(chromedp.Evaluate(js, &ok))
		if !ok {
			t.Fatalf("browser assertion failed: %s", js)
		}
	}
	nav := func(path string) {
		t.Helper()
		run(chromedp.Navigate(strings.TrimRight(*uiBrowserURL, "/")+path), chromedp.WaitReady(".js-enabled", chromedp.ByQuery))
		check(`document.querySelector('meta[name="csrf-token"]').content === 'fixture-csrf'`)
	}
	wait := func(js string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var ready bool
			err := chromedp.Run(ctx, chromedp.Evaluate(js, &ready))
			if err == nil && ready {
				return
			}
			if err != nil && !strings.Contains(err.Error(), "context") {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("browser wait timed out: %s", js)
	}
	nav("/app/jobs")
	check(`document.querySelectorAll('.js-job-check').length === 50 && Array.from(document.querySelectorAll('.js-triage-action')).every(x=>x.disabled)`)
	eval(`document.getElementById('select-all-jobs').click()`)
	check(`Array.from(document.querySelectorAll('.js-triage-action')).every(x=>!x.disabled) && document.getElementById('selected-jobs-count').textContent.includes('50')`)
	eval(`Array.from(document.querySelectorAll('.js-job-check')).slice(25).forEach(x=>x.click())`)
	check(`Array.from(document.querySelectorAll('.js-triage-action')).every(x=>!x.disabled) && document.getElementById('select-all-jobs').indeterminate`)
	wait(`document.getElementById('selected-jobs-count').textContent.includes('25 selected across pages')`)
	nav("/app/jobs?page=2")
	check(`document.getElementById('selected-jobs-count').textContent.includes('25 selected across pages')`)
	nav("/app/jobs")
	check(`document.querySelectorAll('.js-job-check:checked').length === 25 && document.getElementById('selected-jobs-count').textContent.includes('25 selected across pages')`)
	nav("/app/jobs?q=Example&page=2")
	check(`document.querySelectorAll('.js-job-check').length === 14`)
	eval(`document.querySelector('.jobs-table .job-link').click()`)
	wait(`document.title.startsWith('Job Detail')`)
	check(`document.querySelector('.breadcrumbs a').href.includes('page=2') && document.querySelector('.breadcrumbs a').href.includes('q=Example')`)
	nav("/app/applications")
	eval(`document.querySelector('.js-app-check[value="990006"]').click()`)
	check(`!document.querySelector('button[formaction$="/review"]').disabled && document.querySelector('button[formaction$="send-confirm"]').hidden && document.querySelector('button[formaction$="/remove"]').hidden`)
	nav("/app/applications?fixture_gmail=1")
	eval(`document.querySelector('.js-app-check[value="990003"]').click()`)
	check(`document.querySelector('button[formaction$="/draft"]').classList.contains('primary') && !document.querySelector('button[formaction$="/draft"]').disabled`)
	nav("/app/applications")
	eval(`document.querySelector('.js-app-check[value="990009"]').click();document.querySelector('button[formaction$="send-confirm"]').click()`)
	wait(`!!document.querySelector('input[name="send_confirm"]')`)
	check(`document.querySelector('form[action$="/bulk/send"] button[type=submit]').disabled`)
	eval(`document.querySelector('input[name="send_confirm"]').click()`)
	check(`!document.querySelector('form[action$="/bulk/send"] button[type=submit]').disabled`)
	// Leave the final Send action untouched.
	dir := *uiScreens
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "screens")
	}
	os.MkdirAll(dir, 0700)
	for _, size := range [][2]int64{{1536, 1024}, {1366, 768}, {390, 844}} {
		var shot []byte
		run(chromedp.EmulateViewport(size[0], size[1]), chromedp.CaptureScreenshot(&shot))
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("send-%d.png", size[0])), shot, 0600)
		check(`document.documentElement.scrollWidth === innerWidth`)
	}
	nav("/app/applications/review?ids=990006,990015")
	check(`document.querySelector('button[type=submit]').disabled`)
	eval(`document.querySelector('textarea[name=review_note]').closest('details').open=true;document.querySelector('textarea[name=review_note]').focus()`)
	run(chromedp.KeyEvent("j"))
	check(`document.querySelector('textarea[name=review_note]').value === 'j'`)
	eval(`document.querySelector('input[name=review_confirm]').click()`)
	check(`!document.querySelector('form[action$="/approve"] button[type=submit]').disabled`)
	// Real confirmation endpoint, but only for a fixture application.
	eval(`document.querySelector('form[action$="/approve"] button').click()`)
	wait(`document.querySelector('.company')?.textContent.includes('15')`)
	nav("/app/applications/easy-apply?ids=990001")
	check(`document.getElementById('easy-confirm-form').dataset.opened === 'false' && document.querySelector('input[name=apply_confirm]').disabled`)
	eval(`window.open=()=>null;document.querySelector('#easy-open-form button').click()`)
	wait(`document.getElementById('easy-open-status').textContent.includes('blocked')`)
	check(`document.getElementById('easy-confirm-form').dataset.opened === 'false' && document.querySelector('input[name=apply_confirm]').disabled`)
	// Stub only the tab; exercise the actual CSRF-protected local open handler.
	eval(`window.open=()=>({opener:null,location:{href:''},close(){}});document.querySelector('#easy-open-form button').click()`)
	wait(`document.getElementById('easy-confirm-form').dataset.opened === 'true'`)
	check(`document.querySelector('#easy-confirm-form button').disabled && document.getElementById('easy-state').textContent === 'In progress'`)
	eval(`document.querySelector('input[name=apply_confirm]').click();document.querySelector('#easy-confirm-form button').click()`)
	wait(`location.search.includes('easy_applied=1')`)
	nav("/app/applications/990001")
	check(`document.querySelector('.badge').title === 'APPLIED'`)
	nav("/app/settings?tab=email")
	check(`document.getElementById('settings-email').classList.contains('active') && !document.querySelector('input[readonly]')`)
	eval(`document.querySelector('.mobile-menu summary').click()`)
	check(`document.querySelector('.mobile-menu').open && document.querySelector('.mobile-menu a[href="/app/jobs"]') !== null`)
	run(chromedp.KeyEvent(kb.Escape))
	check(`!document.querySelector('.mobile-menu').open`)
	nav("/app/collect")
	// Prevent navigation at the document boundary, after the app submit listener.
	eval(`document.addEventListener('submit',e=>e.preventDefault());document.getElementById('collect-form').requestSubmit(document.getElementById('collect-submit'))`)
	check(`document.getElementById('collect-form').dataset.busy === 'true' && document.getElementById('collect-submit').textContent === 'Collecting…'`)
	eval(`document.getElementById('collect-form').requestSubmit(document.getElementById('collect-submit'))`)
	check(`document.querySelectorAll('#collect-form .action-status').length === 1`)

	// Read-only sweep of every major route and important variant at requested sizes.
	paths := []string{"/app/dashboard", "/app/jobs", "/app/jobs/990020", "/app/jobs/990003", "/app/applications", "/app/applications/990000", "/app/applications/990003?fixture_gmail=1", "/app/applications/990015?fixture_gmail=1", "/app/applications/990009?fixture_gmail=1", "/app/applications/990012", "/app/applications/990004", "/app/applications/990010", "/app/applications/review?fixture_gmail=1", "/app/applications/easy-apply", "/app/applications/easy-apply?ids=990016", "/app/cv-profiles", "/app/collect", "/app/settings", "/app/settings?tab=email", "/app/jobs?q=nomatchingjobs", "/app/applications?state=NOT_APPLIED", "/app/collect?collect=done&searched=20&new=12&persisted=10&exact=8&reposts=2", "/app/collect?collect_error=Choose+a+result+limit+between+1+and+100", "/app/jobs/missing"}
	for _, size := range [][2]int64{{1536, 1024}, {1366, 768}, {390, 844}} {
		run(chromedp.EmulateViewport(size[0], size[1]))
		for _, path := range paths {
			nav(path)
			check(`document.documentElement.scrollWidth === innerWidth`)
			check(`Array.from(document.querySelectorAll('input:not([type=hidden]),select,textarea')).every(e=>e.labels?.length||e.getAttribute('aria-label'))`)
			check(`Array.from(document.querySelectorAll('[id]')).map(e=>e.id).every((id,i,a)=>a.indexOf(id)===i)`)
			if *uiScreens != "" {
				var shot []byte
				run(chromedp.CaptureScreenshot(&shot))
				name := strings.NewReplacer("/", "-", "?", "-", "&", "-", "=", "-").Replace(strings.TrimPrefix(path, "/app/"))
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.png", name, size[0])), shot, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Log("Verified selection limits, eligibility, pagination/filter return, review keyboard/approval, send gate, manual Easy Apply, blocked popups, mobile navigation, and duplicate-submit protection")
}
