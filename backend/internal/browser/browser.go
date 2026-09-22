// Package browser decides whether a request is a person looking at a page in a
// browser, or a program that wants data.
//
// The strongest signal is Fetch Metadata (`Sec-Fetch-*`). Those are forbidden
// header names, so page JavaScript cannot set or forge them, and a browser
// distinguishes its own cases for us: a typed URL or a followed link is
// `Sec-Fetch-Dest: document`, while the page's own fetch() for JSON is
// `Sec-Fetch-Dest: empty`. That is exactly the distinction this service needs,
// and it is far more reliable than reading User-Agent.
//
// Not every browser sends them — Safari only did so from 16.4 — so User-Agent
// remains the fallback, and known bots and HTTP libraries are excluded first
// because several of them advertise a browser-shaped User-Agent.
package browser

import (
	"net/http"
	"regexp"
	"strings"
)

// engines covers the rendering engines and their mobile/iOS variants. On iOS
// every browser is WebKit but keeps its own token (CriOS, FxiOS, EdgiOS).
var engines = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`Chrome/`, `CriOS/`, `Chromium/`, // Chrome, Chrome iOS, Chromium
	`Firefox/`, `FxiOS/`, `Focus/`, `Iceweasel/`, // Firefox family
	`Safari/`, `AppleWebKit/`, // Safari and WebKit shells
	`Edg/`, `EdgA/`, `EdgiOS/`, `Edge/`, // Edge: desktop, Android, iOS, legacy
	`OPR/`, `OPiOS/`, `Opera`, `Opera Mini`, // Opera family
	`SamsungBrowser/`, `YaBrowser/`, `Vivaldi/`, // vendor forks
	`UCBrowser/`, `QQBrowser/`, `MiuiBrowser/`, `HuaweiBrowser/`,
	`DuckDuckGo/`, `Brave/`, `SeaMonkey/`, `Konqueror/`, `Epiphany/`,
	`Trident/`, `MSIE `, // legacy IE
	`Gecko/`, `Presto/`, // engine tokens
}, "|"))

// tools are non-browsers, listed first because many send a Mozilla/5.0 prefix
// and would otherwise match the engine patterns above. Crawlers and link
// unfurlers belong here too: they are programs, and they should get the data.
//
// Entries are deliberately anchored to how these agents actually write
// themselves — a name plus a version separator — rather than loose substrings.
// Misclassifying a bot as a browser is harmless (it gets the page it did not
// want); misclassifying a browser as a bot means a person sees plain text, so
// the patterns err towards letting an unknown agent through. An unknown agent
// still gets plain text by default, because it will not match `engines`.
var tools = regexp.MustCompile(`(?i)` + strings.Join([]string{
	// command line and libraries
	`curl/`, `Wget/`, `HTTPie/`, `libwww`, `lwp-`, `python-requests`,
	`python-urllib`, `aiohttp`, `httpx/`, `Go-http-client`, `okhttp`,
	`axios/`, `node-fetch`, `undici`, `superagent`, `Java/`,
	`Apache-HttpClient`, `Faraday`, `Guzzle`, `PHP/`, `\bRuby\b`,
	`PowerShell`, `WinHttp`, `Dart/`, `Deno/`, `Bun/`, `Scrapy`,
	`RestSharp`, `HttpClient`,
	// api clients and monitors
	`PostmanRuntime`, `Insomnia`, `k6/`, `Pingdom`, `UptimeRobot`,
	`StatusCake`, `Site24x7`, `Zabbix`, `Nagios`, `check_http`,
	`Prometheus`, `Datadog`, `NewRelic`, `vegeta`,
	// search and seo crawlers
	`Googlebot`, `bingbot`, `Slurp`, `DuckDuckBot`, `Baiduspider`,
	`YandexBot`, `Sogou`, `Exabot`, `facebot`, `ia_archiver`, `Applebot`,
	`PetalBot`, `SeznamBot`, `AhrefsBot`, `SemrushBot`, `MJ12bot`,
	`DotBot`, `BLEXBot`, `DataForSeoBot`, `Bytespider`, `Barkrowler`,
	// ai and llm crawlers
	`GPTBot`, `ChatGPT-User`, `OAI-SearchBot`, `ClaudeBot`, `Claude-Web`,
	`anthropic-ai`, `PerplexityBot`, `CCBot`, `Google-Extended`,
	`Amazonbot`, `meta-externalagent`, `cohere-ai`, `Diffbot`,
	// link unfurlers
	`Slackbot`, `Twitterbot`, `facebookexternalhit`, `TelegramBot`,
	`Discordbot`, `LinkedInBot`, `redditbot`, `SkypeUriPreview`,
	`vkShare`, `Embedly`, `Iframely`,
	// headless and generic markers, kept tight on purpose
	`HeadlessChrome`, `PhantomJS`, `Lighthouse`,
	`\bbot\b`, `\bcrawler\b`, `\bspider\b`, `\bscraper\b`,
}, "|"))

// IsBot reports whether the User-Agent identifies a program rather than a
// person's browser.
func IsBot(ua string) bool { return ua != "" && tools.MatchString(clamp(ua)) }

// maxUA bounds what reaches the regexes.
//
// RE2 is linear in input times program size, and these two patterns are large:
// measured at roughly 2.8us per byte. net/http accepts a 1 MiB header by
// default, so an unbounded User-Agent bought an attacker about three seconds of
// CPU for one request. No real agent is anywhere near this long, and a bot that
// pads past it is a bot either way.
const maxUA = 512

func clamp(ua string) string {
	if len(ua) > maxUA {
		return ua[:maxUA]
	}
	return ua
}

// looksLikeBrowserUA reports whether the User-Agent names a browser engine.
//
// The caller has already ruled out bots; repeating IsBot here doubled the cost
// of the more expensive of the two patterns on every request.
func looksLikeBrowserUA(ua string) bool {
	return ua != "" && engines.MatchString(clamp(ua))
}

// IsNavigation reports whether this request is a browser loading a page to
// show a person, as opposed to a script, a subresource fetch, or a crawler.
func IsNavigation(h http.Header) bool {
	ua := h.Get("User-Agent")
	if IsBot(ua) {
		return false
	}

	// Fetch Metadata is authoritative when present: the browser is telling us
	// directly what kind of load this is, and JavaScript cannot override it.
	if dest := strings.ToLower(strings.TrimSpace(h.Get("Sec-Fetch-Dest"))); dest != "" {
		switch dest {
		case "document", "iframe", "frame", "embed", "object":
			return true
		default:
			// "empty" (fetch/XHR), "script", "style", "image", "font", ...
			return false
		}
	}

	// No Fetch Metadata: fall back to the User-Agent, but still require the
	// caller to actually want HTML, so a browser's fetch() for JSON on an older
	// Safari is not mistaken for a navigation.
	if !looksLikeBrowserUA(ua) {
		return false
	}
	if WantsJSON(h) {
		return false
	}
	accept := h.Get("Accept")
	if strings.Contains(accept, "text/html") || strings.Contains(accept, "application/xhtml") {
		return true
	}
	// Some older browsers send a bare Accept on navigation but still advertise
	// the upgrade preference, which no HTTP library sets.
	return h.Get("Upgrade-Insecure-Requests") == "1"
}

// WantsJSON reports whether the caller explicitly asked for JSON.
func WantsJSON(h http.Header) bool {
	return strings.Contains(h.Get("Accept"), "application/json")
}
