package gpu

import "testing"

// The prefix is the whole method, so the cases are the real strings
// drivers report, including the trademark noise Intel puts in its.
func TestVendorOfModelReadsTheDriverPrefix(t *testing.T) {
	cases := map[string]string{
		"NVIDIA GeForce RTX 4090":                VendorNVIDIA,
		"NVIDIA H100 80GB HBM3":                  VendorNVIDIA,
		"Intel(R) Arc(TM) A770 Graphics":         VendorIntel,
		"Intel(R) Data Center GPU Flex 170":      VendorIntel,
		"AMD Radeon RX 7900 XTX":                 VendorAMD,
		"Advanced Micro Devices Instinct MI300X": VendorAMD,
		"  nvidia geforce gtx 1080 ":             VendorNVIDIA,
		"Some Unknown Accelerator":               "",
		"":                                       "",
	}
	for model, want := range cases {
		if got := VendorOfModel(model); got != want {
			t.Errorf("VendorOfModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestVendorOfClassCoversEveryKnownClassAndNothingElse(t *testing.T) {
	for _, class := range Classes() {
		if v := VendorOfClass(class); !Known(v) {
			t.Errorf("class %q maps to unknown vendor %q", class, v)
		}
	}
	if VendorOfClass("not-a-class") != "" {
		t.Error("an unknown class must map to no vendor, not a default")
	}
	if VendorOfClass("RTX-4090") != VendorNVIDIA {
		t.Error("class lookup should be case-insensitive, as class.go's matching is")
	}
}

func TestKnownIsClosed(t *testing.T) {
	for _, v := range Vendors() {
		if !Known(v) {
			t.Errorf("Vendors() lists %q but Known rejects it", v)
		}
	}
	if Known("apple") || Known("") {
		t.Error("Known accepted a vendor the pool cannot render for")
	}
}
