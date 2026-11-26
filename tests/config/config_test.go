package config

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
)

func fixture(t testing.TB) string {
	t.Helper()
	body, err := os.ReadFile("testdata/app.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

func TestConfig(t *testing.T) {
	raw := fixture(t)
	app, err := config.Parse([]byte(raw))
	if err != nil || app.Name != "smc_demo" || len(app.Functions) != 4 || len(app.Buckets["b_shared"]) != 2 {
		t.Fatalf("TEST-APP-01: app=%+v err=%v", app, err)
	}
	t.Run("TEST-APP-08", func(t *testing.T) {
		app, err := config.Parse([]byte(strings.Replace(raw, "processor.so", "producer.so", 1)))
		if err != nil || app.Functions["processor"].SOName != app.Functions["producer"].SOName {
			t.Fatalf("one SO should serve both functions: %v", err)
		}
	})

	mutations := []struct{ id, from, to string }{
		{"TEST-APP-02/syntax", "{", "["},
		{"TEST-APP-02/trailing", "\"smc_demo\",", "\"smc_demo\",,"},
		{"TEST-APP-02/duplicate", "\"app_name\":", "\"app_name\": \"other\", \"app_name\":"},
		{"TEST-APP-02/case", "\"app_name\"", "\"App_Name\""},
		{"TEST-APP-02/unknown", "\"app_name\"", "\"execution\""},
		{"TEST-APP-02/long_string", "smc_demo", strings.Repeat("a", 4097)},
		{"TEST-APP-04", "\"functions\": [", "\"functions\": [null,"},
		{"TEST-APP-04/external", "\"functions\": [", "\"functions\": [\"functions.json\","},
		{"TEST-APP-05", "\"so_name\": \"producer.so\",", ""},
		{"TEST-APP-06", "\"func_name\": \"producer\",", ""},
		{"TEST-APP-07", "\"func_name\": \"processor\"", "\"func_name\": \"producer\""},
		{"TEST-APP-10/trigger", "\"function\": \"producer\"", "\"function\": \"unknown\""},
		{"TEST-APP-10/direct", "\"direct_source\"\n      ],", "\"unknown\"\n      ],"},
		{"TEST-APP-11/parent", "producer.so", "../producer.so"},
		{"TEST-APP-11/windows", "producer.so", "root\\\\producer.so"},
		{"TEST-APP-12/zero_cpu", "\"cpu_cores\": 1", "\"cpu_cores\": 0"},
		{"TEST-APP-12/cpu_overflow", "\"cpu_cores\": 1", "\"cpu_cores\": 1e500"},
		{"TEST-APP-12/zero_memory", "\"memory_mb\": 128", "\"memory_mb\": 0"},
		{"TEST-APP-13/count", "\"device_count\": 0", "\"device_count\": -1"},
		{"TEST-APP-13/gpu", "\"memory_mb\": 0", "\"memory_mb\": -1"},
		{"TEST-APP-13/disk", "\"disk_mb\": 0", "\"disk_mb\": -1"},
		{"TEST-APP-13/network", "\"network_mbps\": 0", "\"network_mbps\": -1"},
		{"TEST-APP-15/count_overflow", "\"device_count\": 0", "\"device_count\": 4294967296"},
		{"TEST-APP-15/count_fraction", "\"device_count\": 0", "\"device_count\": 1.1"},
		{"TEST-APP-15/memory_overflow", "\"memory_mb\": 128", "\"memory_mb\": 17592186044416"},
		{"TEST-APP-15/gpu_overflow", "\"memory_mb\": 0", "\"memory_mb\": 17592186044416"},
		{"TEST-APP-15/disk_overflow", "\"disk_mb\": 0", "\"disk_mb\": 18446744073709551616"},
		{"TEST-APP-15/time_overflow", "\"max_wait_ms\": 1000", "\"max_wait_ms\": 9223372036855"},
		{"TEST-APP-15/time_order", "\"max_wait_ms\": 1000", "\"max_wait_ms\": 9"},
		{"TEST-APP-15/null", "\"enabled\": true", "\"enabled\": null"},
		{"TEST-APP-15/missing_enabled", "\"enabled\": true,", ""},
		{"TEST-DIR-09/null", "\"trigger_option\": 0", "\"trigger_option\": null"},
		{"TEST-DIR-09/negative", "\"trigger_option\": 0", "\"trigger_option\": -1"},
		{"TEST-DIR-09/fraction", "\"trigger_option\": 0", "\"trigger_option\": 1.0"},
		{"TEST-DIR-09/overflow", "\"trigger_option\": 0", "\"trigger_option\": 3"},
		{"TEST-DIR-09/duplicate_pair", "\"direct_processor\"\n      ]", "\"direct_processor\", \"direct_processor\"\n      ]"},
		{"TEST-EVT-07/dependency", "\"DIRECT\"", "\"K_OUT_OF_N\""},
		{"TEST-EVT-07/trigger", "\"IMMEDIATE\"", "\"BY_SET\""},
		{"TEST-EVT-07/batch", "\"IMMEDIATE\"", "\"BY_BATCH_SIZE\""},
		{"TEST-EVT-07/key_name_missing", "\"key_name\": \"ready\"", "\"key_name\": \"\""},
		{"TEST-EVT-07/key_name_forbidden", "\"function\": \"producer\"", "\"function\": \"producer\", \"key_name\": \"x\""},
		{"TEST-RTE-29/path_missing", "\"disk_mb\": 0", "\"disk_mb\": 1"},
		{"TEST-RTE-30/domain_missing", "\"network_mbps\": 0", "\"network_mbps\": 1"},
	}
	for _, tt := range mutations {
		t.Run(tt.id, func(t *testing.T) {
			changed := strings.Replace(raw, tt.from, tt.to, 1)
			if changed == raw {
				t.Fatalf("fixture no longer contains %q", tt.from)
			}
			if app, err := config.Parse([]byte(changed)); app != nil || err == nil {
				t.Fatalf("invalid config accepted: app=%+v err=%v", app, err)
			}
		})
	}

	t.Run("TEST-APP-15/exact_limits", func(t *testing.T) {
		raw := strings.NewReplacer(
			"\"memory_mb\": 128", "\"memory_mb\": 17592186044415",
			"\"max_wait_ms\": 1000", "\"max_wait_ms\": 9223372036854",
			"\"device_count\": 0", "\"device_count\": 4294967295",
		).Replace(raw)
		app, err := config.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		got := app.Functions["producer"].Resource
		if got.MemoryMB != math.MaxUint64/config.MiB || got.GPU.DeviceCount != math.MaxUint32 ||
			app.Delay.MaxWait != time.Duration(9223372036854)*time.Millisecond {
			t.Fatalf("boundaries lost precision: %+v, %+v", got, app.Delay)
		}
	})
	t.Run("TEST-DIR-08/modes", func(t *testing.T) {
		for _, option := range []string{"", ", \"trigger_option\": 0", ", \"trigger_option\": 1", ", \"trigger_option\": 2"} {
			test := strings.Replace(raw, "],\n      \"trigger_option\": 0", "]"+option, 1)
			app, err := config.Parse([]byte(test))
			if err != nil {
				t.Fatal(err)
			}
			var want contract.InputMode
			if option != "" {
				want = contract.InputMode(option[len(option)-1] - '0')
			}
			if got := app.Direct["direct_source"][0].Mode; got != want {
				t.Fatalf("option %q mapped to %d, want %d", option, got, want)
			}
		}
	})
}

func FuzzConfig(f *testing.F) {
	body := fixture(f)
	for _, seed := range []string{body, "", "null", "{}", `{"app_name":"a","app_name":"b"}`,
		strings.Replace(body, "1000", "9223372036855", 1), strings.Replace(body, "128", "18446744073709551616", 1),
		strings.Replace(body, "IMMEDIATE", "BY_SET", 1), strings.Replace(body, "producer.so", "../producer.so", 1),
		strings.Replace(body, "smc_demo", strings.Repeat("x", 4097), 1)} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		app, err := config.Parse(data)
		if (err == nil) != (app != nil) {
			t.Fatalf("partial publication candidate: app=%+v error=%v", app, err)
		}
		if app != nil && (app.Name == "" || len(app.Functions) == 0 || app.Delay.MaxWait < app.Delay.RetryInterval) {
			t.Fatal("accepted invalid app shape")
		}
	})
}

func TestByNamePreservesExactKey(t *testing.T) {
	raw := strings.Replace(fixture(t), `"key_name": "ready"`, `"key_name": " "`, 1)
	app, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("one-space key is nonempty: %v", err)
	}
	if got := app.Buckets["b_shared"][1].Key; got != " " {
		t.Fatalf("BY_NAME changed key to %q", got)
	}
}
