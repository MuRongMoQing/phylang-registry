package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestIntegratedFrontendAndBackend(t *testing.T) {
	src := `import physics.classical;
let m=measured(2 kg,0.01 kg);
let v=measured(3 [m/s],0.02 [m/s]);
print 0.5*m*v^2 in J;
print backend_run("x*x+1","x=3");
law Candidate { parameters { force: Force; mass: Mass; acceleration: Acceleration; } assumptions { mass>0 kg; } equation { force==mass*acceleration; } }
verify Candidate against NewtonSecondLaw with { force=6 N; mass=2 kg; acceleration=3 [m/s^2]; };`
	var out bytes.Buffer
	if _, err := runFrontendSource(src, "<test>", &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, expected := range []string{"9", "J", "10", "Verification: Candidate", "[PASS] Sample equations"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %s", expected, text)
		}
	}
}

func TestUnitConversionPrecedence(t *testing.T) {
	env := newFEnv()
	if _, err := runFrontendSource("let m=2 kg; let v=3 [m/s]; print 0.5*m*v^2 in J;", "<test>", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	v, err := evalFrontendExpr("0.5*2 kg*(3 [m/s])^2 in J", env)
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 9 || v.PreferredUnit != "J" {
		t.Fatalf("got %#v", v)
	}
}

func TestSemanticCompletions(t *testing.T) {
	items := completionItems("let velocity=3 [m/s]; fn kinetic(m,v){ return m*v^2/2; }", "ve")
	if !containsString(items, "velocity") {
		t.Fatalf("items=%v", items)
	}
	items = completionItems("law CustomLaw {", "Cus")
	if !containsString(items, "CustomLaw") {
		t.Fatalf("items=%v", items)
	}
}

func TestStudioSameOriginAPI(t *testing.T) {
	if err := studioSelfTest(); err != nil {
		t.Fatal(err)
	}
}

func TestIfElseAndWhile(t *testing.T) {
	src := `let x=0;
while (x<3) { x=x+1; }
if (x==3) { print x; } else { print 99; }`
	var out bytes.Buffer
	if _, err := runFrontendSource(src, "<control>", &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "3" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestQuantityUnitConstAndFunctionBody(t *testing.T) {
	src := `quantity Speed2 : Length / Time;
unit furlong_per_fortnight : Speed2 = 0.000166309524 [m/s];
const base = 2 furlong_per_fortnight;
fn double_speed(x) { let y=x*2; if (y>0 [m/s]) { return y; } return 0 [m/s]; }
print double_speed(base) in [m/s];`
	var out bytes.Buffer
	if _, err := runFrontendSource(src, "<features>", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0.000665238096") {
		t.Fatalf("output=%s", out.String())
	}
	if _, err := runFrontendSource("const x=1; x=2;", "<const>", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "不可重新赋值") {
		t.Fatalf("expected const error, got %v", err)
	}
}

func TestPackageImportAndPrivacy(t *testing.T) {
	root := t.TempDir()
	pkg := root + "/community.demo"
	if err := InitPackage(pkg, "community.demo"); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadPackageManifest(pkg)
	if err != nil {
		t.Fatal(err)
	}
	manifestText, err := os.ReadFile(manifest.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = manifestText
	// Add a private constant used by an exported function; it must stay available through the function closure.
	source := `package community.demo { version "0.1.0"; requires phylang ">=0.6.0 <0.7.0"; }
const private_factor=3;
export quantity ExampleRate : Length / Time;
export unit example_rate : ExampleRate = 1 [m/s];
export fn scale_example(x) { let y=x*private_factor; return y; }
export law ExampleLaw { parameters { d: Length; t: Time; v: ExampleRate; } assumptions { t>0 s; } equation { d==v*t; } }`
	if err = os.WriteFile(pkg+"/src/package.phy", []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	program := `import community.demo; print scale_example(2 example_rate) in [m/s]; verify ExampleLaw with { d=10 m; t=5 s; v=2 [m/s]; };`
	old := os.Getenv("PHYLANG_HOME")
	defer os.Setenv("PHYLANG_HOME", old)
	os.Setenv("PHYLANG_HOME", root+"/home")
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err = os.Chdir(pkg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err = runFrontendSource(program, "<import>", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "6 m/s") || !strings.Contains(out.String(), "[PASS] Sample equations") {
		t.Fatalf("output=%s", out.String())
	}
}
