package prompt

import "testing"

func TestCleanHumanName(t *testing.T) {
	tests := []struct {
		first    string
		last     string
		expected string
	}{
		{"Riya", "Sharma", "Riya Sharma"},
		{"🌸 Priya 🌸", "", "Priya"},
		{"⚡ Sumit ⚡", "Verma", "Sumit Verma"},
		{"John", "Doe", "John Doe"},
		{"", "Kaur", "Kaur"},
		{"", "", ""},
		{"User", "", ""},
		{"Deleted Account", "", ""},
		{"Admin", "User", ""},
		{"Rahul-Kumar", "Singh", "Rahul-Kumar Singh"},
	}

	for _, tc := range tests {
		got := CleanHumanName(tc.first, tc.last)
		if got != tc.expected {
			t.Errorf("CleanHumanName(%q, %q) = %q, expected %q", tc.first, tc.last, got, tc.expected)
		}
	}
}

func TestGetCallName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Riya Sharma", "Riya"},
		{"Aman", "Aman"},
		{"  Sneha   Verma  ", "Sneha"},
		{"", ""},
	}

	for _, tc := range tests {
		got := GetCallName(tc.input)
		if got != tc.expected {
			t.Errorf("GetCallName(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestDetectGenderFromText(t *testing.T) {
	tests := []struct {
		message  string
		expected string
	}{
		{"mai ladki hu", "female"},
		{"mai ladka hu", "male"},
		{"karti hoon", "female"},
		{"karta hoon", "male"},
		{"hello how are you", ""},
	}

	for _, tc := range tests {
		got := DetectGenderFromText(tc.message)
		if got != tc.expected {
			t.Errorf("DetectGenderFromText(%q) = %q, expected %q", tc.message, got, tc.expected)
		}
	}
}
