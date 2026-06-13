package monitor

import "testing"

func TestNewIPFilterInvalid(t *testing.T) {
	cases := [][]string{
		{"not-an-ip"},
		{"10.0.0.0/99"},
		{"1.2.3.4", "bad/cidr/x"},
	}
	for _, entries := range cases {
		if _, err := newIPFilter(entries); err == nil {
			t.Errorf("newIPFilter(%v): ожидалась ошибка, получено nil", entries)
		}
	}
}

func TestIPFilterMatch(t *testing.T) {
	f, err := newIPFilter([]string{
		" 203.0.113.5 ",
		"10.0.0.0/8",
		"2001:db8::/32",
		"",
	})
	if err != nil {
		t.Fatalf("newIPFilter: %v", err)
	}
	if f.empty() {
		t.Fatal("фильтр не должен быть пустым")
	}

	cases := []struct {
		ip   string
		want bool
	}{
		{"203.0.113.5", true},
		{"203.0.113.6", false},
		{"10.1.2.3", true},
		{"11.0.0.1", false},
		{"2001:db8::1", true},
		{"2001:dead::1", false},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		if got := f.Match(tc.ip); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIPFilterEmpty(t *testing.T) {
	f, err := newIPFilter(nil)
	if err != nil {
		t.Fatalf("newIPFilter(nil): %v", err)
	}
	if !f.empty() {
		t.Fatal("пустой фильтр должен возвращать empty() == true")
	}
	if f.Match("1.2.3.4") {
		t.Error("пустой фильтр не должен сопоставлять никакие IP")
	}
}
