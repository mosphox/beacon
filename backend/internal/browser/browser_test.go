package browser

import (
	"net/http"
	"testing"
)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			h.Set(kv[i], kv[i+1])
		}
	}
	return h
}

// Real User-Agent strings across engines, platforms and versions. A browser
// navigating must get the app whatever it is running on.
var browserUAs = []string{
	// Chrome: macOS, Windows, Linux, Android, iOS
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0 Mobile/15E148 Safari/604.1",
	// Safari: macOS, iPhone, iPad
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
	// Firefox: Windows, Linux, Android, iOS
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0",
	"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/115.0",
	"Mozilla/5.0 (Android 14; Mobile; rv:130.0) Gecko/130.0 Firefox/130.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/127.0 Mobile/15E148 Safari/605.1.15",
	// Edge: desktop, Android, iOS
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
	"Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36 EdgA/131.0.0.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) EdgiOS/121.0 Mobile/15E148 Safari/605.1.15",
	// Other engines and vendor forks
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0",
	"Mozilla/5.0 (Linux; Android 13; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/23.0 Chrome/115.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 YaBrowser/24.9.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Vivaldi/6.8",
	"Mozilla/5.0 (Windows NT 10.0; WOW64; Trident/7.0; rv:11.0) like Gecko",
}

var toolUAs = []string{
	"curl/8.7.1",
	"Wget/1.21.4",
	"HTTPie/3.2.2",
	"python-requests/2.32.3",
	"Go-http-client/2.0",
	"okhttp/4.12.0",
	"axios/1.7.7",
	"node-fetch/3.3.2",
	"Java/17.0.2",
	"PostmanRuntime/7.42.0",
	"Apache-HttpClient/5.3 (Java/17)",
	"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
	"Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)",
	"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)",
	"Mozilla/5.0 (compatible; GPTBot/1.0; +https://openai.com/gptbot)",
	"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0)",
	"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)",
	"Twitterbot/1.0",
	"facebookexternalhit/1.1",
	"TelegramBot (like TwitterBot)",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/131.0.0.0 Safari/537.36",
}

const navAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func TestBrowserNavigationsGetThePage(t *testing.T) {
	for _, ua := range browserUAs {
		t.Run(ua[:min(44, len(ua))], func(t *testing.T) {
			// Modern browsers: Fetch Metadata present.
			h := hdr("User-Agent", ua, "Accept", navAccept,
				"Sec-Fetch-Dest", "document", "Sec-Fetch-Mode", "navigate")
			if !IsNavigation(h) {
				t.Error("with Sec-Fetch-Dest: document → not a navigation")
			}
			// Older browsers: no Fetch Metadata, fall back to UA + Accept.
			h = hdr("User-Agent", ua, "Accept", navAccept)
			if !IsNavigation(h) {
				t.Error("without Fetch Metadata → not a navigation")
			}
		})
	}
}

func TestToolsNeverGetThePage(t *testing.T) {
	for _, ua := range toolUAs {
		t.Run(ua[:min(40, len(ua))], func(t *testing.T) {
			// Even when a tool claims to want HTML, or forges Fetch Metadata.
			for _, h := range []http.Header{
				hdr("User-Agent", ua, "Accept", "*/*"),
				hdr("User-Agent", ua, "Accept", navAccept),
				hdr("User-Agent", ua, "Accept", navAccept,
					"Sec-Fetch-Dest", "document", "Sec-Fetch-Mode", "navigate"),
			} {
				if IsNavigation(h) {
					t.Errorf("tool treated as a navigation: %v", h)
				}
			}
		})
	}
}

// The page's own fetch() must get data, not the page — this is the case that
// would otherwise loop forever.
func TestBrowserFetchGetsData(t *testing.T) {
	ua := browserUAs[0]
	cases := []http.Header{
		// fetch() for JSON, with Fetch Metadata as a browser really sends it.
		hdr("User-Agent", ua, "Accept", "application/json",
			"Sec-Fetch-Dest", "empty", "Sec-Fetch-Mode", "cors"),
		hdr("User-Agent", ua, "Accept", "application/json",
			"Sec-Fetch-Dest", "empty", "Sec-Fetch-Mode", "same-origin"),
		// Older browser without Fetch Metadata, but explicit about JSON.
		hdr("User-Agent", ua, "Accept", "application/json"),
	}
	for i, h := range cases {
		if IsNavigation(h) {
			t.Errorf("case %d: browser fetch treated as a navigation", i)
		}
		if !WantsJSON(h) {
			t.Errorf("case %d: WantsJSON = false", i)
		}
	}
}

// Subresource loads must never be answered with the page.
func TestSubresourcesAreNotNavigations(t *testing.T) {
	ua := browserUAs[0]
	for _, dest := range []string{"empty", "script", "style", "image", "font", "manifest", "audio", "video"} {
		h := hdr("User-Agent", ua, "Accept", navAccept, "Sec-Fetch-Dest", dest)
		if IsNavigation(h) {
			t.Errorf("Sec-Fetch-Dest: %s treated as a navigation", dest)
		}
	}
	for _, dest := range []string{"document", "iframe", "frame"} {
		h := hdr("User-Agent", ua, "Accept", navAccept, "Sec-Fetch-Dest", dest)
		if !IsNavigation(h) {
			t.Errorf("Sec-Fetch-Dest: %s not treated as a navigation", dest)
		}
	}
}

func TestNoUserAgent(t *testing.T) {
	if IsNavigation(hdr("Accept", navAccept)) {
		t.Error("empty User-Agent treated as a navigation")
	}
}

// A browser sending a bare Accept on navigation still signals the upgrade
// preference, which no HTTP library sets.
func TestUpgradeInsecureRequestsFallback(t *testing.T) {
	h := hdr("User-Agent", browserUAs[0], "Accept", "*/*", "Upgrade-Insecure-Requests", "1")
	if !IsNavigation(h) {
		t.Error("browser with Upgrade-Insecure-Requests not treated as a navigation")
	}
	h = hdr("User-Agent", "curl/8.7.1", "Accept", "*/*", "Upgrade-Insecure-Requests", "1")
	if IsNavigation(h) {
		t.Error("curl forging Upgrade-Insecure-Requests treated as a navigation")
	}
}

func TestIsBot(t *testing.T) {
	for _, ua := range toolUAs {
		if !IsBot(ua) {
			t.Errorf("IsBot(%q) = false, want true", ua)
		}
	}
	for _, ua := range browserUAs {
		if IsBot(ua) {
			t.Errorf("IsBot(%q) = true, want false", ua)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Real browsers that a loose substring match would misfile as bots. Getting
// this wrong sends a person plain text instead of the app.
func TestLegacyAndInAppBrowsersAreNotBots(t *testing.T) {
	for _, ua := range []string{
		"Mozilla/5.0 (compatible; MSIE 8.0; Windows NT 6.1; Trident/4.0; .NET CLR 2.0.50727)",
		"Mozilla/5.0 (Windows NT 6.1; Trident/7.0; .NET4.0C; .NET4.0E; rv:11.0) like Gecko",
		"Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36 WhatsApp/2.24",
		"Mozilla/5.0 (Macintosh) AppleWebKit/537.36 Abbot/1.0 Chrome/131.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36 Monitoring/1.0",
	} {
		if IsBot(ua) {
			t.Errorf("IsBot(%q) = true, want false", ua)
		}
		h := hdr("User-Agent", ua, "Accept", navAccept, "Sec-Fetch-Dest", "document")
		if !IsNavigation(h) {
			t.Errorf("real browser denied the page: %q", ua)
		}
	}
}
