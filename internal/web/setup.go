package web

import "strings"

// Platforms offered when creating a project. The ID is stored on the project
// and picks the setup steps on its settings page.
var platforms = []struct{ ID, Name string }{
	{"javascript", "JavaScript (Browser)"},
	{"react", "React"},
	{"node", "Node.js"},
	{"react-native", "React Native"},
	{"python", "Python"},
	{"go", "Go"},
	{"ruby", "Ruby / Rails"},
	{"php", "PHP"},
	{"laravel", "Laravel"},
	{"java", "Java"},
	{"android", "Kotlin / Android"},
	{"dotnet", ".NET / ASP.NET Core"},
	{"unity", "Unity"},
	{"rust", "Rust"},
	{"elixir", "Elixir"},
	{"flutter", "Dart / Flutter"},
	{"apple", "Swift / Apple platforms"},
	{"roblox", "Roblox (Luau)"},
	{"shell", "Shell / cron"},
	{"other", "Other"},
}

// platformName is the display name for a stored platform ID.
func platformName(id string) string {
	for _, p := range platforms {
		if p.ID == id {
			return p.Name
		}
	}
	if id == "" {
		return "Other"
	}
	return id
}

// SetupStep is one labelled code block on the settings page.
type SetupStep struct {
	Label string
	Code  string
}

// Setup is everything the settings page shows for one platform.
type Setup struct {
	Steps []SetupStep
	Note  string // shown under the steps
	SDK   bool   // uses a Sentry SDK (false: the report API)
}

const sdkNote = "Burrow keeps errors only, so leave performance tracing off (it's off unless you turn it on)."

// setupFor returns the setup steps for platform with the project's DSN,
// report URL and key filled in. Roblox has its own template.
func setupFor(platform, dsn, reportURL, key string) Setup {
	s, ok := setups[platform]
	if !ok {
		s = setups["other"]
	}
	r := strings.NewReplacer("__DSN__", dsn, "__REPORT_URL__", reportURL, "__KEY__", key)
	out := Setup{Note: r.Replace(s.Note), SDK: s.SDK}
	for _, st := range s.Steps {
		out.Steps = append(out.Steps, SetupStep{Label: st.Label, Code: r.Replace(st.Code)})
	}
	return out
}

var setups = map[string]Setup{
	"javascript": {SDK: true, Steps: []SetupStep{
		{"Install", "npm install @sentry/browser"},
		{"Initialize before the rest of your app loads", `import * as Sentry from "@sentry/browser";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
	}, Note: "Browsers post straight to Burrow, so it must be reachable from your users' machines, not just your network. " + sdkNote},

	"react": {SDK: true, Steps: []SetupStep{
		{"Install", "npm install @sentry/react"},
		{"Initialize in your entry file, before rendering", `import * as Sentry from "@sentry/react";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
		{"Catch render errors with an error boundary", `<Sentry.ErrorBoundary fallback={<p>Something went wrong.</p>}>
  <App />
</Sentry.ErrorBoundary>`},
	}, Note: "Browsers post straight to Burrow, so it must be reachable from your users' machines. " + sdkNote},

	"node": {SDK: true, Steps: []SetupStep{
		{"Install", "npm install @sentry/node"},
		{"Create instrument.js", `const Sentry = require("@sentry/node");

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});`},
		{"Load it before anything else", `// First line of your entry file:
require("./instrument");
const Sentry = require("@sentry/node");

// Express only: after your routes, before other error handlers
Sentry.setupExpressErrorHandler(app);`},
	}, Note: "Uncaught exceptions and unhandled rejections are reported without extra code. " + sdkNote},

	"react-native": {SDK: true, Steps: []SetupStep{
		{"Install", "npm install @sentry/react-native"},
		{"Initialize in App.js and wrap the root component", `import * as Sentry from "@sentry/react-native";

Sentry.init({
  dsn: "__DSN__",
  environment: "production",
});

export default Sentry.wrap(App);`},
	}, Note: "iOS and Android block plain-HTTP requests by default, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"python": {SDK: true, Steps: []SetupStep{
		{"Install", "pip install --upgrade sentry-sdk"},
		{"Initialize as early as possible", `import sentry_sdk

sentry_sdk.init(
    dsn="__DSN__",
    environment="production",
)`},
	}, Note: "Django, Flask, FastAPI, Celery and most other frameworks are picked up automatically. " + sdkNote},

	"go": {SDK: true, Steps: []SetupStep{
		{"Install", "go get github.com/getsentry/sentry-go"},
		{"Initialize in main", `import (
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
	}, Note: "For net/http, wrap handlers with sentryhttp from github.com/getsentry/sentry-go/http so panics in requests are reported. " + sdkNote},

	"ruby": {SDK: true, Steps: []SetupStep{
		{"Install", `bundle add sentry-ruby
bundle add sentry-rails   # Rails only`},
		{"Initialize (in Rails: config/initializers/sentry.rb)", `Sentry.init do |config|
  config.dsn = "__DSN__"
  config.environment = "production"
end`},
	}, Note: "With sentry-rails, controller and Active Job exceptions are reported automatically. " + sdkNote},

	"php": {SDK: true, Steps: []SetupStep{
		{"Install", "composer require sentry/sentry"},
		{"Initialize as early as possible", `\Sentry\init([
    'dsn' => '__DSN__',
    'environment' => 'production',
]);

// Report an exception you caught:
// \Sentry\captureException($e);`},
	}, Note: "Uncaught exceptions and fatal errors are reported once init has run. " + sdkNote},

	"laravel": {SDK: true, Steps: []SetupStep{
		{"Install and write the DSN to .env", `composer require sentry/sentry-laravel
php artisan sentry:publish --dsn=__DSN__`},
		{"Laravel 11+: report exceptions in bootstrap/app.php", `use Sentry\Laravel\Integration;

->withExceptions(function (Exceptions $exceptions) {
    Integration::handles($exceptions);
})`},
	}, Note: "sentry:publish offers to send a test event; it goes to Burrow, so you can accept it. " + sdkNote},

	"java": {SDK: true, Steps: []SetupStep{
		{"Add the dependency (Gradle; for Maven use io.sentry:sentry)", `implementation("io.sentry:sentry:8.+")`},
		{"Initialize at startup", `import io.sentry.Sentry;

Sentry.init(options -> {
    options.setDsn("__DSN__");
    options.setEnvironment("production");
});

// Report an exception you caught:
// Sentry.captureException(e);`},
	}, Note: "Spring Boot: use io.sentry:sentry-spring-boot-starter-jakarta and set sentry.dsn in application.properties instead. " + sdkNote},

	"android": {SDK: true, Steps: []SetupStep{
		{"Add the dependency (app/build.gradle.kts)", `implementation("io.sentry:sentry-android:8.+")`},
		{"Set the DSN in AndroidManifest.xml, inside <application>", `<meta-data
    android:name="io.sentry.dsn"
    android:value="__DSN__" />
<meta-data
    android:name="io.sentry.environment"
    android:value="production" />`},
		{"Kotlin on the JVM (no Android): use io.sentry:sentry and", `Sentry.init { options ->
    options.dsn = "__DSN__"
    options.environment = "production"
}`},
	}, Note: "Android blocks plain-HTTP requests by default, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"dotnet": {SDK: true, Steps: []SetupStep{
		{"Install (ASP.NET Core: Sentry.AspNetCore)", `dotnet add package Sentry`},
		{"Console or desktop app: initialize in Main", `using var sentry = SentrySdk.Init(options =>
{
    options.Dsn = "__DSN__";
    options.Environment = "production";
});`},
		{"ASP.NET Core: in Program.cs", `builder.WebHost.UseSentry(options =>
{
    options.Dsn = "__DSN__";
    options.Environment = "production";
});`},
	}, Note: sdkNote},

	"unity": {SDK: true, Steps: []SetupStep{
		{"Window → Package Manager → + → Add package from git URL", "https://github.com/getsentry/unity.git"},
		{"Tools → Sentry → Options, then paste the DSN", "__DSN__"},
	}, Note: "Unhandled C# exceptions are reported from the editor and from builds. Mobile and console builds block plain HTTP, so give Burrow an https:// base URL before shipping. " + sdkNote},

	"rust": {SDK: true, Steps: []SetupStep{
		{"Install", "cargo add sentry"},
		{"Initialize first thing in main and keep the guard alive", `fn main() {
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
		{"Add to deps in mix.exs", `{:sentry, "~> 10.0"},
{:hackney, "~> 1.20"}`},
		{"Configure in config/prod.exs", `config :sentry,
  dsn: "__DSN__",
  environment_name: :prod`},
		{"Report crashed processes: in Application.start/2", `:logger.add_handler(:sentry_handler, Sentry.LoggerHandler, %{
  config: %{metadata: [:file, :line]}
})`},
	}, Note: "Phoenix: also add use Sentry.PlugCapture to your endpoint. " + sdkNote},

	"flutter": {SDK: true, Steps: []SetupStep{
		{"Install (plain Dart: dart pub add sentry)", "flutter pub add sentry_flutter"},
		{"Wrap your app in main.dart", `import 'package:flutter/widgets.dart';
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

	"apple": {SDK: true, Steps: []SetupStep{
		{"Xcode → File → Add Package Dependencies…, then paste", "https://github.com/getsentry/sentry-cocoa"},
		{"Start it in your App's init (or application(_:didFinishLaunchingWithOptions:))", `import Sentry

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

	"shell": {Steps: []SetupStep{
		{"Add to the top of a bash script or cron job (needs curl and jq)", `burrow_report() {
  jq -n --arg m "$1" --arg s "$2" --arg h "$(hostname)" \
    '{message: $m, stack: $s, fingerprint: ["{{ default }}", $s],
      platform: "shell", server_name: $h}' |
  curl -fsS -m 10 "__REPORT_URL__" \
    -H "X-Burrow-Key: __KEY__" -H "Content-Type: application/json" \
    --data-binary @- >/dev/null || true
}
trap 'burrow_report "$BASH_COMMAND exited with status $?" "$0:$LINENO"' ERR`},
		{"Or report something yourself", `burrow_report "Backup to S3 failed" "$0:$LINENO"`},
	}, Note: "Every failing command is reported with its script and line, and each failing line is its own issue. A failure inside a function is reported where the function was called. Uses the report API, not a Sentry SDK."},

	"other": {Steps: []SetupStep{
		{"Any language with an HTTP client: POST JSON to the report API", `curl "__REPORT_URL__" \
  -H "X-Burrow-Key: __KEY__" \
  -H "Content-Type: application/json" \
  -d '{"message": "Payment failed for order 1182", "level": "error",
       "stack": "...", "environment": "production"}'`},
	}, Note: "If your language has an official Sentry SDK, initialize it with the DSN above instead; Burrow speaks the same protocol. Otherwise the report API below takes plain JSON from anything."},
}
