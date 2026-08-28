package main

import (
	"context"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/gioui-plugins/gio-plugins/hyperlink/giohyperlink"
	"github.com/gioui-plugins/gio-plugins/plugin/gioplugins"
	"github.com/gioui-plugins/gio-plugins/webviewer/giowebview"
	"github.com/guohuiyuan/go-music-dl/internal/appshell"

	_ "gioui.org/app/permission/storage"
	_ "gioui.org/app/permission/wakelock"
)

type webTag struct{}

type desktopApp struct {
	window *app.Window
	ops    op.Ops
	tag    webTag

	// 这些状态用于在多个 frame 之间协调 WebView 初始化流程，
	// 因为 Gio 插件命令是在 frame 处理中下发的。
	bridgeInstalled       bool
	callbackRegistered    bool
	storagePermissionOnce bool
	bundledFFmpegOnce     bool
	pendingInitialNav     bool
	pendingHistoryBack    bool
	pendingExternalOpenTo *url.URL
	initialNav            <-chan initialNavigationResult
	initialNavURL         string
	initialNavReady       bool
	initialNavSentAt      time.Time
	initialNavAcked       bool
	currentWebURL         string
	reloadPending         bool
	reloadDeferred        bool
	playbackActive        bool
	lastFrameAt           time.Time
}

const (
	downloadCallback        = "musicDlOpenDownload"
	playbackCallback        = "musicDlPlaybackState"
	appStateCallback        = "musicDlAppState"
	preferredBrowserPK      = ""
	initialNavRetryInterval = 3 * time.Second
	resumeReloadThreshold   = 2 * time.Second
)

type initialNavigationResult struct {
	URL string
	Err error
}

// 注入的桥接脚本把桌面端行为放在壳层处理：
// 返回操作继续走原生外壳，普通链接下载模式下的下载则回传给 Go 处理。
const bridgeScript = `(function () {
  if (window.__musicDlDesktopBridgeInstalled) {
    return;
  }
  window.__musicDlDesktopBridgeInstalled = true;

  document.addEventListener("keydown", function (event) {
    if (event.defaultPrevented || event.isComposing) {
      return;
    }
    if (event.key === "BrowserBack") {
      event.preventDefault();
      window.history.back();
      return;
    }
    if (event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && event.key === "ArrowLeft") {
      event.preventDefault();
      window.history.back();
    }
  }, true);

  function notifyPlaybackState(state) {
    if (globalThis.callback && typeof globalThis.callback.musicDlPlaybackState === "function") {
      globalThis.callback.musicDlPlaybackState("playback:" + state);
    }
  }

  function notifyAppState(state) {
    if (globalThis.callback && typeof globalThis.callback.musicDlAppState === "function") {
      globalThis.callback.musicDlAppState("state:" + state);
    }
  }

  document.addEventListener("DOMContentLoaded", function () {
    notifyAppState("loaded");
  });

  document.addEventListener("play", function (event) {
    if (event.target && event.target.tagName === "AUDIO") {
      notifyPlaybackState("playing");
    }
  }, true);

  document.addEventListener("pause", function (event) {
    if (event.target && event.target.tagName === "AUDIO") {
      notifyPlaybackState("paused");
    }
  }, true);

  document.addEventListener("ended", function (event) {
    if (event.target && event.target.tagName === "AUDIO") {
      notifyPlaybackState("ended");
    }
  }, true);

  window.addEventListener("pagehide", function () {
    notifyPlaybackState("released");
  });

  function bindAPlayerPlayback() {
    var player = window.ap;
    if (!player || !player.audio || player.audio.__musicDlPlaybackBound) {
      return;
    }
    var audio = player.audio;
    audio.__musicDlPlaybackBound = true;
    function notifyCurrentPlaybackState(state) {
      if (window.ap && window.ap.audio === audio) {
        notifyPlaybackState(state);
      }
    }
    audio.addEventListener("playing", function () {
      notifyCurrentPlaybackState("playing");
    });
    audio.addEventListener("pause", function () {
      notifyCurrentPlaybackState("paused");
    });
    audio.addEventListener("ended", function () {
      notifyCurrentPlaybackState("ended");
    });
  }

  function bindAPlayerWhenReady() {
    bindAPlayerPlayback();
    if (!window.ap || !window.ap.audio) {
      setTimeout(bindAPlayerWhenReady, 250);
    }
  }
  bindAPlayerWhenReady();
  setInterval(bindAPlayerPlayback, 500);

  document.addEventListener("click", function (event) {
    if (event.defaultPrevented) {
      return;
    }
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }

    var link = event.target && event.target.closest ? event.target.closest(".btn-download, .btn-browser-download") : null;
    if (!link) {
      return;
    }

    if (link.classList.contains("btn-download")) {
      return;
    }

    var href = String(link.href || "").trim();
    if (!href) {
      return;
    }
    try {
      var url = new URL(href, window.location.href);
      url.searchParams.delete("save_local");
      href = url.toString();
    } catch (_) {}

    event.preventDefault();
    event.stopPropagation();

    if (globalThis.callback && typeof globalThis.callback.musicDlOpenDownload === "function") {
      globalThis.callback.musicDlOpenDownload(href);
    } else {
      window.location.href = href;
    }
  }, true);
})();`

const historyBackScript = `if (window.history.length > 1) { window.history.back(); }`

func main() {
	path, err := app.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	os.Setenv("MUSIC_DL_CONFIG_DB", path+"/settings.db")
	os.Setenv("MUSIC_DL_COOKIE_FILE", path+"/cookies.json")

	initialNav := startInitialNavigation(appshell.DefaultPort)

	go func() {
		window := new(app.Window)
		window.Option(app.Title("music-dl"))
		if err := newDesktopApp(window, initialNav).run(); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func newDesktopApp(window *app.Window, initialNav <-chan initialNavigationResult) *desktopApp {
	return &desktopApp{
		window:     window,
		initialNav: initialNav,
	}
}

func (a *desktopApp) run() error {
	for {
		switch evt := gioplugins.Hijack(a.window).(type) {
		case app.DestroyEvent:
			a.setPlaybackWakeLock(false)
			return evt.Err
		case app.ViewEvent:
			a.configureBundledFFmpeg(evt)
			a.requestStoragePermission(evt)
		case app.FrameEvent:
			a.handleFrame(evt)
		}
	}
}

func (a *desktopApp) handleFrame(evt app.FrameEvent) {
	gtx := app.NewContext(&a.ops, evt)

	now := time.Now()
	if a.initialNavAcked && !a.lastFrameAt.IsZero() && now.Sub(a.lastFrameAt) > resumeReloadThreshold {
		// iOS WKWebView can come back from background with a blank native layer.
		// A long frame gap is the only lifecycle signal available through Gio here.
		if a.playbackActive {
			a.reloadDeferred = true
		} else {
			a.reloadPending = true
		}
	}
	a.lastFrameAt = now

	a.pendingHistoryBack = a.pendingHistoryBack || consumeBackShortcuts(gtx)
	a.consumeWebViewEvents(gtx)
	a.consumeInitialNavigationResult()
	a.layoutWebView(gtx)
	evt.Frame(gtx.Ops)

	a.ensureBridge(gtx)
	a.handlePendingNavigation(gtx)
	a.handlePendingReload(gtx)
	a.handleInitialNavigationRecovery(gtx)
	a.handlePendingHistoryBack(gtx)
	a.handlePendingExternalOpen(gtx)
}

func (a *desktopApp) layoutWebView(gtx layout.Context) {
	size := gtx.Constraints.Max
	stack := giowebview.WebViewOp{Tag: &a.tag}.Push(gtx.Ops)
	giowebview.RectOp{Size: f32.Point{X: float32(size.X), Y: float32(size.Y)}}.Add(gtx.Ops)
	stack.Pop(gtx.Ops)
}

func (a *desktopApp) ensureBridge(gtx layout.Context) {
	if !a.bridgeInstalled {
		gioplugins.Execute(gtx, giowebview.InstallJavascriptCmd{
			View:   &a.tag,
			Script: bridgeScript,
		})
		a.bridgeInstalled = true
	}

	if a.bridgeInstalled && !a.callbackRegistered {
		// 只注册一次 JS 回调，这样下载链接会交回桌面壳层处理，
		// 而不是在内嵌 WebView 里继续跳转。
		gioplugins.Execute(gtx, giowebview.MessageReceiverCmd{
			View: &a.tag,
			Tag:  &a.tag,
			Name: downloadCallback,
		})
		gioplugins.Execute(gtx, giowebview.MessageReceiverCmd{
			View: &a.tag,
			Tag:  &a.tag,
			Name: playbackCallback,
		})
		gioplugins.Execute(gtx, giowebview.MessageReceiverCmd{
			View: &a.tag,
			Tag:  &a.tag,
			Name: appStateCallback,
		})
		a.callbackRegistered = true
		a.pendingInitialNav = true
		a.window.Invalidate()
	}
}

func (a *desktopApp) handlePendingNavigation(gtx layout.Context) {
	if !a.pendingInitialNav || !a.initialNavReady {
		return
	}

	// 首次跳转延后到桥接回调准备完成之后，避免页面过早加载。
	a.navigateTo(gtx, a.navigationTarget())
	a.pendingInitialNav = false
}

func (a *desktopApp) handlePendingReload(gtx layout.Context) {
	if !a.reloadPending || !a.initialNavReady || !a.callbackRegistered {
		return
	}

	a.navigateTo(gtx, a.navigationTarget())
}

func (a *desktopApp) handleInitialNavigationRecovery(gtx layout.Context) {
	if !a.initialNavReady || !a.callbackRegistered || a.pendingInitialNav || a.initialNavAcked {
		return
	}
	if a.initialNavSentAt.IsZero() || time.Since(a.initialNavSentAt) < initialNavRetryInterval {
		return
	}

	// NavigateCmd can be dropped before the native WebView exists, so keep
	// retrying until the page explicitly confirms that it loaded.
	a.navigateTo(gtx, a.navigationTarget())
}

func (a *desktopApp) navigateTo(gtx layout.Context, rawURL string) {
	if rawURL == "" {
		return
	}

	gioplugins.Execute(gtx, giowebview.NavigateCmd{
		URL:  rawURL,
		View: &a.tag,
	})
	a.initialNavSentAt = time.Now()
	a.initialNavAcked = false
	a.reloadPending = false
}

func (a *desktopApp) navigationTarget() string {
	if a.currentWebURL != "" {
		return a.currentWebURL
	}
	return a.initialNavURL
}

func (a *desktopApp) consumeInitialNavigationResult() {
	if a.initialNavReady {
		return
	}
	if a.initialNav == nil {
		a.initialNavURL = appshell.AppURL(appshell.DefaultPort)
		a.initialNavReady = true
		return
	}

	select {
	case result := <-a.initialNav:
		if result.URL == "" {
			result.URL = appshell.AppURL(appshell.DefaultPort)
		}
		if result.Err != nil {
			log.Printf("desktop server startup probe failed: %v", result.Err)
		}
		a.initialNavURL = result.URL
		a.initialNavReady = true
		if a.callbackRegistered {
			a.pendingInitialNav = true
		}
		if a.window != nil {
			a.window.Invalidate()
		}
	default:
	}
}

func (a *desktopApp) handlePendingHistoryBack(gtx layout.Context) {
	if !a.pendingHistoryBack || !a.bridgeInstalled || a.pendingInitialNav {
		return
	}

	gioplugins.Execute(gtx, giowebview.ExecuteJavascriptCmd{
		View:   &a.tag,
		Script: historyBackScript,
	})
	a.pendingHistoryBack = false
}

func (a *desktopApp) handlePendingExternalOpen(gtx layout.Context) {
	if a.pendingExternalOpenTo == nil {
		return
	}

	// 通过系统浏览器打开链接，可以直接复用服务端现有的 /download 行为，
	// 不需要继续扩展 WebView 插件本身。
	gioplugins.Execute(gtx, giohyperlink.OpenCmd{
		Tag:              &a.tag,
		URI:              a.pendingExternalOpenTo,
		PreferredPackage: preferredBrowserPK,
	})
	log.Printf("opened download in external browser: %s", a.pendingExternalOpenTo.String())
	a.pendingExternalOpenTo = nil
}

func (a *desktopApp) consumeWebViewEvents(gtx layout.Context) {
	for {
		evt, ok := gioplugins.Event(gtx, giowebview.Filter{Target: &a.tag})
		if !ok {
			return
		}

		switch evt := evt.(type) {
		case giowebview.MessageEvent:
			a.handleWebViewMessage(evt.Message)
		case giowebview.NavigationEvent:
			a.handleNavigationEvent(evt.URL)
		}
	}
}

func (a *desktopApp) handleWebViewMessage(raw string) {
	if strings.HasPrefix(raw, "playback:") {
		a.handlePlaybackState(strings.TrimPrefix(raw, "playback:"))
		return
	}
	if strings.HasPrefix(raw, "state:") {
		a.handleWebViewState(strings.TrimPrefix(raw, "state:"))
		return
	}

	u, err := url.Parse(raw)
	if err != nil {
		log.Printf("invalid download url from webview: %q (%v)", raw, err)
		return
	}

	a.pendingExternalOpenTo = u
	log.Printf("received download url from webview: %s", u.String())
}

func (a *desktopApp) handleNavigationEvent(rawURL string) {
	if rawURL != "" {
		a.currentWebURL = rawURL
	}
	if strings.HasPrefix(rawURL, "data:") {
		// Data URLs are not injected with the JS bridge, so the URL change is
		// the only load acknowledgement available for the startup error page.
		a.initialNavAcked = true
		a.reloadPending = false
	}
}

func (a *desktopApp) handleWebViewState(state string) {
	switch strings.TrimSpace(state) {
	case "loaded":
		a.initialNavAcked = true
		a.reloadPending = false
	}
}

func (a *desktopApp) handlePlaybackState(state string) {
	switch strings.TrimSpace(state) {
	case "playing":
		a.playbackActive = true
		a.reloadPending = false
		a.reloadDeferred = false
		a.setPlaybackWakeLock(true)
	case "paused", "ended", "released":
		a.playbackActive = false
		if a.reloadDeferred {
			a.reloadDeferred = false
			a.reloadPending = true
		}
		a.setPlaybackWakeLock(false)
	}
}

func consumeBackShortcuts(gtx layout.Context) bool {
	handled := false
	for {
		evt, ok := gtx.Event(
			key.Filter{Name: key.NameBack},
			key.Filter{Name: key.NameLeftArrow, Required: key.ModAlt},
		)
		if !ok {
			return handled
		}

		ke, ok := evt.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		handled = true
	}
}

func startInitialNavigation(port string) <-chan initialNavigationResult {
	ch := make(chan initialNavigationResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), appshell.ReadyTimeout)
		defer cancel()

		target, err := appshell.StartDesktopServerAndWait(ctx, port)
		if err != nil {
			ch <- initialNavigationResult{
				URL: appshell.StartupErrorDataURL(err.Error(), target),
				Err: err,
			}
			return
		}
		ch <- initialNavigationResult{URL: target}
	}()
	return ch
}
