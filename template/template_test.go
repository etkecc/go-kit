package template

import (
	"testing"
)

// TestExecuteSuccess tests the success scenario of Execute function
func TestExecuteSuccess(t *testing.T) {
	tplString := "Hello, {{.Name}}!"
	vars := map[string]string{"Name": "Alice"}

	result, err := Execute(tplString, vars)
	if err != nil {
		t.Fatalf("Execute() returned an unexpected error: %v", err)
	}
	if result != "Hello, Alice!" {
		t.Errorf("Execute() = %v, want %v", result, "Hello, Alice!")
	}
}

// TestExecuteTemplateParseError tests the case where template parsing fails
func TestExecuteTemplateParseError(t *testing.T) {
	tplString := "Hello, {{.Name" // malformed template

	result, err := Execute(tplString, nil)

	if err == nil {
		t.Fatalf("Expected error, got nil")
	}
	if result != "" {
		t.Errorf("Expected empty result, got %v", result)
	}
}

// TestExecuteTemplateExecutionError tests the case where template execution fails
func TestExecuteTemplateExecutionError(t *testing.T) {
	tplString := "Hello, {{.Name}}"
	vars := struct{}{} // no Name field, so it should fail

	result, err := Execute(tplString, vars)

	if err == nil {
		t.Fatalf("Expected execution error, got nil")
	}
	if result != "" {
		t.Errorf("Expected empty result, got %v", result)
	}
}

// TestMaySuccess tests May with a successful template execution
func TestMaySuccess(t *testing.T) {
	tplString := "Hello, {{.Name}}!"
	vars := map[string]string{"Name": "Alice"}

	result := May(tplString, vars)

	if result != "Hello, Alice!" {
		t.Errorf("May() = %v, want %v", result, "Hello, Alice!")
	}
}

// TestMayFallback tests May with a parsing error and fallback to the original template string
func TestMayFallback(t *testing.T) {
	tplString := "Hello, {{.Name" // malformed template

	result := May(tplString, nil)

	if result != tplString {
		t.Errorf("May() = %v, want %v", result, tplString)
	}
}

// TestMustSuccess tests Must with a successful template execution
func TestMustSuccess(t *testing.T) {
	tplString := "Hello, {{.Name}}!"
	vars := map[string]string{"Name": "Alice"}

	result := Must(tplString, vars)

	if result != "Hello, Alice!" {
		t.Errorf("Must() = %v, want %v", result, "Hello, Alice!")
	}
}

// TestMustPanic tests Must with a parsing error that causes a panic
func TestMustPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic, got none")
		}
	}()

	tplString := "Hello, {{.Name" // malformed template

	Must(tplString, nil)
}
