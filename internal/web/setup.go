package web

import "strings"

type platform struct{ ID, Name string }

// platformGroups are the platforms offered when creating a project, grouped
// so the list scans. The ID is stored on the project and picks the setup
// steps on its settings page.
var platformGroups = []struct {
	Name      string
	Platforms []platform
}{
	{"Web", []platform{
		{"javascript", "JavaScript (Browser)"},
		{"react", "React"},
		{"node", "Node.js"},
	}},
	{"Backend", []platform{
		{"python", "Python"},
		{"go", "Go"},
		{"ruby", "Ruby / Rails"},
		{"php", "PHP"},
		{"laravel", "Laravel"},
		{"java", "Java"},
		{"dotnet", ".NET / ASP.NET Core"},
		{"rust", "Rust"},
		{"elixir", "Elixir"},
	}},
	{"Mobile and games", []platform{
		{"react-native", "React Native"},
		{"android", "Kotlin / Android"},
		{"apple", "Swift / Apple platforms"},
		{"flutter", "Dart / Flutter"},
		{"unity", "Unity"},
		{"roblox", "Roblox (Luau)"},
	}},
	{"No SDK", []platform{
		{"shell", "Shell / cron"},
		{"other", "Other (plain HTTP)"},
	}},
}

// platformName is the display name for a stored platform ID.
func platformName(id string) string {
	for _, g := range platformGroups {
		for _, p := range g.Platforms {
			if p.ID == id {
				return p.Name
			}
		}
	}
	if id == "" {
		return "Other"
	}
	return id
}

// SetupStep is one labelled code block on the settings page.
type SetupStep struct {
	Label    string
	Code     string
	Help     string // optional line under the label
	Download string // optional file name; the step can then be downloaded
	Tall     bool   // long enough that the page shows it in a scrolling box
}

// Setup is everything the settings page shows for one platform.
type Setup struct {
	Intro string // optional paragraph above the steps
	Steps []SetupStep
	Note  string // shown under the steps
	SDK   bool   // uses a Sentry SDK (false: the report API)
	// Reach says where the code will run, so the page can warn when the
	// server's address won't be reachable from there: "public" (needs a
	// public https:// address), "https" (needs https), "network" (needs an
	// address other machines can reach) or "" (anywhere).
	Reach string
}

// tallLines is the length past which a step's code scrolls in place
// instead of pushing the rest of the page down.
const tallLines = 24

const sdkNote = "Burrow keeps errors only, so leave performance tracing off (it's off unless you turn it on)."

// setupFor returns the setup steps for platform with the project's DSN,
// report URL and key filled in. Roblox has its own template.
func setupFor(platform, dsn, reportURL, key string) Setup {
	s, ok := setups[platform]
	if !ok {
		s = setups["other"]
	}
	r := strings.NewReplacer("__DSN__", dsn, "__REPORT_URL__", reportURL, "__KEY__", key)
	out := Setup{Intro: s.Intro, Note: r.Replace(s.Note), SDK: s.SDK, Reach: s.Reach}
	for _, st := range s.Steps {
		st.Code = r.Replace(st.Code)
		st.Tall = strings.Count(st.Code, "\n") >= tallLines
		out.Steps = append(out.Steps, st)
	}
	return out
}

var setups = map[string]Setup{
	"javascript": {SDK: true, Reach: "network", Steps: []SetupStep{
		{Label: "Install", Code: "npm install @sentry/browser"},
		{Label: "Initialize before the rest of your app loads", Code: `import * as Sentry from "@sentry/browser";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
	}, Note: "Browsers post straight to Burrow, so it must be reachable from your users' machines, not just your network. " + sdkNote},

	"react": {SDK: true, Reach: "network", Steps: []SetupStep{
		{Label: "Install", Code: "npm install @sentry/react"},
		{Label: "Initialize in your entry file, before rendering", Code: `import * as Sentry from "@sentry/react";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
		{Label: "Catch render errors with an error boundary", Code: `<Sentry.ErrorBoundary fallback={<p>Something went wrong.</p>}>
  <App />
</Sentry.ErrorBoundary>`},
	}, Note: "Browsers post straight to Burrow, so it must be reachable from your users' machines. " + sdkNote},

	"node": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: "npm install @sentry/node"},
		{Label: "Create instrument.js", Code: `const Sentry = require("@sentry/node");

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
		{Label: "Load it before anything else", Code: `// First line of your entry file:
require("./instrument");
const Sentry = require("@sentry/node");

// Express only: after your routes, before other error handlers
Sentry.setupExpressErrorHandler(app);`},
	}, Note: "Uncaught exceptions and unhandled rejections are reported without extra code. " + sdkNote},

	"react-native": {SDK: true, Reach: "https", Steps: []SetupStep{
		{Label: "Install", Code: "npm install @sentry/react-native"},
		{Label: "Initialize in App.js and wrap the root component", Code: `import * as Sentry from "@sentry/react-native";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});

export default Sentry.wrap(App);`},
	}, Note: "iOS and Android block plain-HTTP requests by default, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"python": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: "pip install --upgrade sentry-sdk"},
		{Label: "Initialize as early as possible", Code: `import sentry_sdk

sentry_sdk.init(
    dsn="__DSN__",
    environment="production",
)`},
	}, Note: "Django, Flask, FastAPI, Celery and most other frameworks are picked up automatically. " + sdkNote},

	"go": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: "go get github.com/getsentry/sentry-go"},
		{Label: "Initialize in main", Code: `import (
	"log"
	"time"

	"github.com/getsentry/sentry-go"
)

func main() {
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         "__DSN__",
		Environment: "production",
	}); err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}
	defer sentry.Flush(2 * time.Second)
	defer sentry.Recover() // report panics in main

	// Report an error you handled:
	// sentry.CaptureException(err)
}`},
	}, Note: "For `net/http`, wrap handlers with `sentryhttp` from `github.com/getsentry/sentry-go/http` so panics in requests are reported. " + sdkNote},

	"ruby": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: `bundle add sentry-ruby
bundle add sentry-rails   # Rails only`},
		{Label: "Initialize (in Rails: config/initializers/sentry.rb)", Code: `Sentry.init do |config|
  config.dsn = "__DSN__"
  config.environment = "production"
end`},
	}, Note: "With `sentry-rails`, controller and Active Job exceptions are reported automatically. " + sdkNote},

	"php": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: "composer require sentry/sentry"},
		{Label: "Initialize as early as possible", Code: `\Sentry\init([
    'dsn' => '__DSN__',
    'environment' => 'production',
]);

// Report an exception you caught:
// \Sentry\captureException($e);`},
	}, Note: "Uncaught exceptions and fatal errors are reported once init has run. " + sdkNote},

	"laravel": {SDK: true, Steps: []SetupStep{
		{Label: "Install and write the DSN to .env", Code: `composer require sentry/sentry-laravel
php artisan sentry:publish --dsn=__DSN__`},
		{Label: "Laravel 11+: report exceptions in bootstrap/app.php", Code: `use Sentry\Laravel\Integration;

->withExceptions(function (Exceptions $exceptions) {
    Integration::handles($exceptions);
})`},
	}, Note: "`sentry:publish` offers to send a test event; it goes to Burrow, so you can accept it. " + sdkNote},

	"java": {SDK: true, Steps: []SetupStep{
		{Label: "Add the dependency (Gradle; for Maven use io.sentry:sentry)", Code: `implementation("io.sentry:sentry:8.+")`},
		{Label: "Initialize at startup", Code: `import io.sentry.Sentry;

Sentry.init(options -> {
    options.setDsn("__DSN__");
    options.setEnvironment("production");
});

// Report an exception you caught:
// Sentry.captureException(e);`},
	}, Note: "Spring Boot: use `io.sentry:sentry-spring-boot-starter-jakarta` and set `sentry.dsn` in `application.properties` instead. " + sdkNote},

	"android": {SDK: true, Reach: "https", Steps: []SetupStep{
		{Label: "Add the dependency (app/build.gradle.kts)", Code: `implementation("io.sentry:sentry-android:8.+")`},
		{Label: "Set the DSN in AndroidManifest.xml, inside <application>", Code: `<meta-data
    android:name="io.sentry.dsn"
    android:value="__DSN__" />
<meta-data
    android:name="io.sentry.environment"
    android:value="production" />`},
		{Label: "Kotlin on the JVM (no Android): use io.sentry:sentry and", Code: `Sentry.init { options ->
    options.dsn = "__DSN__"
    options.environment = "production"
}`},
	}, Note: "Android blocks plain-HTTP requests by default, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"dotnet": {SDK: true, Steps: []SetupStep{
		{Label: "Install (ASP.NET Core: Sentry.AspNetCore)", Code: `dotnet add package Sentry`},
		{Label: "Console or desktop app: initialize in Main", Code: `using var sentry = SentrySdk.Init(options =>
{
    options.Dsn = "__DSN__";
    options.Environment = "production";
});`},
		{Label: "ASP.NET Core: in Program.cs", Code: `builder.WebHost.UseSentry(options =>
{
    options.Dsn = "__DSN__";
    options.Environment = "production";
});`},
	}, Note: sdkNote},

	"unity": {SDK: true, Reach: "https", Steps: []SetupStep{
		{Label: "Window → Package Manager → + → Add package from git URL", Code: "https://github.com/getsentry/unity.git"},
		{Label: "Tools → Sentry → Options, then paste the DSN", Code: "__DSN__"},
	}, Note: "Unhandled C# exceptions are reported from the editor and from builds. Mobile and console builds block plain HTTP, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"rust": {SDK: true, Steps: []SetupStep{
		{Label: "Install", Code: "cargo add sentry"},
		{Label: "Initialize first thing in main and keep the guard alive", Code: `fn main() {
    let _guard = sentry::init(("__DSN__", sentry::ClientOptions {
        release: sentry::release_name!(),
        environment: Some("production".into()),
        ..Default::default()
    }));

    // Report an error you handled:
    // sentry::capture_error(&err);
}`},
	}, Note: "Panics are reported while the guard is alive. " + sdkNote},

	"elixir": {SDK: true, Steps: []SetupStep{
		{Label: "Add to deps in mix.exs", Code: `{:sentry, "~> 10.0"},
{:hackney, "~> 1.20"}`},
		{Label: "Configure in config/prod.exs", Code: `config :sentry,
  dsn: "__DSN__",
  environment_name: :prod`},
		{Label: "Report crashed processes: in Application.start/2", Code: `:logger.add_handler(:sentry_handler, Sentry.LoggerHandler, %{
  config: %{metadata: [:file, :line]}
})`},
	}, Note: "Phoenix: also add `use Sentry.PlugCapture` to your endpoint. " + sdkNote},

	"flutter": {SDK: true, Reach: "https", Steps: []SetupStep{
		{Label: "Install (plain Dart: dart pub add sentry)", Code: "flutter pub add sentry_flutter"},
		{Label: "Wrap your app in main.dart", Code: `import 'package:flutter/widgets.dart';
import 'package:sentry_flutter/sentry_flutter.dart';

Future<void> main() async {
  await SentryFlutter.init(
    (options) {
      options.dsn = '__DSN__';
      options.environment = 'production';
    },
    appRunner: () => runApp(const MyApp()),
  );
}`},
	}, Note: "iOS and Android block plain-HTTP requests by default, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"apple": {SDK: true, Reach: "https", Steps: []SetupStep{
		{Label: "Xcode → File → Add Package Dependencies…, then paste", Code: "https://github.com/getsentry/sentry-cocoa"},
		{Label: "Start it in your App's init (or application(_:didFinishLaunchingWithOptions:))", Code: `import Sentry

@main
struct MyApp: App {
    init() {
        SentrySDK.start { options in
            options.dsn = "__DSN__"
            options.environment = "production"
        }
    }
    // ...
}`},
	}, Note: "App Transport Security blocks plain HTTP, so give Burrow an https:// base URL. Crashes are sent the next time the app launches. " + sdkNote},

	"roblox": {Reach: "public",
		Intro: "Roblox has no Sentry SDK, so this uses the report API below. It catches every script error on the server and relays errors from players' devices. Turn on Game Settings → Security → Allow HTTP Requests first.",
		Steps: []SetupStep{
			{Label: "ModuleScript named Burrow in ServerScriptService", Download: "Burrow.lua",
				Help: "Also gives server scripts `Burrow.capture(message, stack)` for errors you catch with `pcall`.",
				Code: robloxModule},
			{Label: "Script in ServerScriptService", Code: `require(script.Parent.Burrow).start()`},
			{Label: "LocalScript in StarterPlayer → StarterPlayerScripts", Code: `local ScriptContext = game:GetService("ScriptContext")
local relay = game:GetService("ReplicatedStorage"):WaitForChild("BurrowClientError")

ScriptContext.Error:Connect(function(message, stack)
	relay:FireServer(message, stack)
end)`},
		},
		Note: "Errors caught with `pcall` never reach `ScriptContext.Error`. To report one anyway, call `Burrow.capture(err, debug.traceback())` from a server script.",
	},

	"shell": {Steps: []SetupStep{
		{Label: "Add to the top of a bash script or cron job (needs curl and jq)", Code: `burrow_report() {
  jq -n --arg m "$1" --arg s "$2" --arg h "$(hostname)" \
    '{message: $m, stack: $s, fingerprint: ["{{ default }}", $s],
      platform: "shell", server_name: $h}' |
  curl -fsS -m 10 "__REPORT_URL__" \
    -H "X-Burrow-Key: __KEY__" -H "Content-Type: application/json" \
    --data-binary @- >/dev/null || true
}
trap 'burrow_report "$BASH_COMMAND exited with status $?" "$0:$LINENO"' ERR`},
		{Label: "Or report something yourself", Code: `burrow_report "Backup to S3 failed" "$0:$LINENO"`},
	}, Note: "Every failing command is reported with its script and line, and each failing line is its own issue. A failure inside a function is reported where the function was called. Uses the report API, not a Sentry SDK."},

	"other": {Steps: []SetupStep{
		{Label: "Any language with an HTTP client: POST JSON to the report API", Code: `curl "__REPORT_URL__" \
  -H "X-Burrow-Key: __KEY__" \
  -H "Content-Type: application/json" \
  -d '{"message": "Payment failed for order 1182", "level": "error",
       "stack": "...", "environment": "production"}'`},
	}, Note: "If your language has an official Sentry SDK, initialize it with the DSN above instead; Burrow speaks the same protocol. Otherwise the report API below takes plain JSON from anything."},
}

// robloxModule batches server errors and client errors relayed through a
// RemoteEvent, and stays well inside Roblox's HTTP request budget.
const robloxModule = `-- Sends every server error, plus errors relayed from clients, to Burrow.
-- Lives in ServerScriptService, which clients never receive, so the key stays private.
local HttpService = game:GetService("HttpService")
local ScriptContext = game:GetService("ScriptContext")
local ReplicatedStorage = game:GetService("ReplicatedStorage")
local Players = game:GetService("Players")
local RunService = game:GetService("RunService")

local URL = "__REPORT_URL__"
local KEY = "__KEY__"

local MAX_BATCH = 100   -- the most one request may carry; extra errors wait for the next flush
local FLUSH_EVERY = 5   -- seconds; keeps well under Roblox's 500 HTTP requests per minute
local CLIENT_LIMIT = 10 -- errors per player per minute, so one client can't flood you

local Burrow = {}
local queue, pending = {}, {}

-- Queue one report. Repeats of the same error before the next flush are
-- counted instead of sent again.
function Burrow.capture(message, stack, level, tags)
	message = string.sub(tostring(message), 1, 2000)
	stack = stack and string.sub(tostring(stack), 1, 8000) or ""
	local id = message .. "\n" .. stack
	if pending[id] then
		pending[id].extra.repeats += 1
		return
	end
	if #queue >= MAX_BATCH * 2 then
		return
	end
	local report = {
		message = message,
		stack = stack,
		level = level or "error",
		platform = "roblox",
		environment = if RunService:IsStudio() then "studio" else "production",
		release = tostring(game.PlaceVersion),
		server_name = game.JobId,
		tags = { place_id = tostring(game.PlaceId), side = "server" },
		extra = { repeats = 1 },
	}
	for k, v in tags or {} do
		report.tags[k] = tostring(v)
	end
	pending[id] = report
	table.insert(queue, report)
end

local function flush()
	if #queue == 0 then
		return
	end
	local batch = {}
	for i = 1, math.min(#queue, MAX_BATCH) do
		table.insert(batch, table.remove(queue, 1))
	end
	pending = {}
	for _, report in queue do
		pending[report.message .. "\n" .. report.stack] = report
	end
	local ok, res = pcall(HttpService.RequestAsync, HttpService, {
		Url = URL,
		Method = "POST",
		Headers = { ["Content-Type"] = "application/json", ["X-Burrow-Key"] = KEY },
		Body = HttpService:JSONEncode(batch),
	})
	if not ok or not res.Success then
		-- warn() goes to the output log, not ScriptContext.Error, so this can't loop.
		warn("Burrow: couldn't send errors:", if ok then res.StatusCode else res)
	end
end

function Burrow.start()
	ScriptContext.Error:Connect(function(message, stack, origin)
		Burrow.capture(message, stack, "error", { script = origin and origin:GetFullName() or "unknown" })
	end)

	-- Clients can't make HTTP requests, so they send their errors here.
	local relay = Instance.new("RemoteEvent")
	relay.Name = "BurrowClientError"
	relay.Parent = ReplicatedStorage
	local budget = {}
	relay.OnServerEvent:Connect(function(player, message, stack)
		if typeof(message) ~= "string" or typeof(stack) ~= "string" then
			return
		end
		local now = os.clock()
		local b = budget[player]
		if not b or now - b.since > 60 then
			b = { since = now, count = 0 }
			budget[player] = b
		end
		b.count += 1
		if b.count > CLIENT_LIMIT then
			return
		end
		Burrow.capture(message, stack, "error", { side = "client", user_id = player.UserId })
	end)
	Players.PlayerRemoving:Connect(function(player)
		budget[player] = nil
	end)

	task.spawn(function()
		while true do
			task.wait(FLUSH_EVERY)
			flush()
		end
	end)
	game:BindToClose(flush)
end

return Burrow`
