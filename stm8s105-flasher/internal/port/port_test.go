package port

import "testing"

func TestSortNames(t *testing.T) {
	names := []string{"COM10", "/dev/ttyUSB1", "COM3", "/dev/ttyUSB0"}
	SortNames(names)
	want := []string{"COM3", "COM10", "/dev/ttyUSB0", "/dev/ttyUSB1"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v want %v", names, want)
		}
	}
}
