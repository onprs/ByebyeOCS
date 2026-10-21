package payments

import "testing"

func TestParseYuanToMinorExact(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
	}{
		{"0.01", 1}, {"5.67", 567}, {"30.00", 3000}, {"1", 100},
		{"001.20", 120}, {"999999999999.99", 99999999999999},
	} {
		got, err := ParseYuanToMinor(test.input)
		if err != nil || got != test.want {
			t.Fatalf("%q => %d,%v，期望 %d", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"", "0", "-1.00", "+1.00", "1.234", "1.", ".5", " 1.00", "1e3", "１.00"} {
		if _, err := ParseYuanToMinor(input); err == nil {
			t.Fatalf("非法金额 %q 被接受", input)
		}
	}
}

func TestFormatYuan(t *testing.T) {
	for _, test := range []struct {
		input int64
		want  string
	}{
		{1, "0.01"}, {567, "5.67"}, {3000, "30.00"}, {100, "1.00"},
	} {
		if got := FormatYuan(test.input); got != test.want {
			t.Fatalf("%d => %q，期望 %q", test.input, got, test.want)
		}
	}
}
