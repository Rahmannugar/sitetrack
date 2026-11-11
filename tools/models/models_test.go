package models

import (
	"context"
	"fmt"
	regel "github.com/sodiqscript111/Regel"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModels(t *testing.T) {
	cases := []struct {
		name      string
		spec      *regel.Spec
		violation string
	}{
		{"Deliveries", deliveries("Deliveries", false, false, false), ""},
		{"SplitStockCheck", deliveries("SplitStockCheck", true, false, false), "NonnegativeStock"},
		{"RepeatedReceipt", deliveries("RepeatedReceipt", false, true, false), "TerminalOnce"},
		{"UnreservedReturn", deliveries("UnreservedReturn", false, false, true), "ReturnCapacity"},
		{"TaskDependencies", tasks("TaskDependencies", false, false), ""},
		{"StaleDependencyState", tasks("StaleDependencyState", true, false), "StoredDependencyState"},
		{"UncheckedReopen", tasks("UncheckedReopen", false, true), "StartedTasksUnblocked"},
		{"ProjectMembership", membership("ProjectMembership", false), ""},
		{"StaleAdminCheck", membership("StaleAdminCheck", true), "AtLeastOneAdmin"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source, err := test.spec.Source()
			if err != nil {
				t.Fatal(err)
			}
			config, err := test.spec.Config()
			if err != nil {
				t.Fatal(err)
			}
			if output := os.Getenv("SITETRACK_MODEL_OUTPUT"); output != "" {
				if err := os.MkdirAll(output, 0755); err != nil {
					t.Fatal(err)
				}
				for extension, body := range map[string]string{".tla": source, ".cfg": config} {
					if err := os.WriteFile(filepath.Join(output, test.name+extension), []byte(body), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			result, err := test.spec.Check(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if test.violation == "" {
				if !result.Passed() {
					t.Fatalf("%s", result)
				}
			} else {
				if result.Verdict != regel.InvariantViolated || result.Violated != test.violation {
					t.Fatalf("expected invariant %s, got %s", test.violation, result)
				}
				if output := os.Getenv("SITETRACK_MODEL_OUTPUT"); output != "" {
					if err := os.WriteFile(filepath.Join(output, test.name+".trace.txt"), []byte(fmt.Sprint(result.Trace)), 0644); err != nil {
						t.Fatal(err)
					}
				}
				t.Logf("Expected counterexample: %s", test.violation)
			}
			t.Logf("verdict=%s generated=%d distinct=%d", result.Verdict, result.StatesGenerated, result.DistinctStates)
		})
	}
}
