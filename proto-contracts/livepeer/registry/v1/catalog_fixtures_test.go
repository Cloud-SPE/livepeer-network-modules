package registryv1

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestCatalogContractFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/catalog-*.json")
	if err != nil || len(paths) != 5 {
		t.Fatalf("fixtures: %v %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var out ListOfferingsResult
			if err := protojson.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if out.Completeness == CatalogCompleteness_CATALOG_COMPLETENESS_UNSPECIFIED {
				t.Fatal("missing completeness")
			}
			for _, e := range out.Entries {
				if e.Expired && e.Selectable {
					t.Fatal("expired offering selectable")
				}
				r := e.Offering
				if r.EthAddress == "" || r.WorkerUrl == "" || e.WorkerId == "" || r.WorkUnitEstimator == nil || r.UnitsPerPrice != 60 || len(r.ExtraJson) == 0 || len(r.ConstraintsJson) == 0 {
					t.Fatal("offering identity/metadata lost")
				}
			}
		})
	}
}
func TestDeferredContractFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/resolution-deferred.json")
	if err != nil {
		t.Fatal(err)
	}
	var st statuspb.Status
	if err := protojson.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Code != 14 || len(st.Details) != 3 {
		t.Fatal(&st)
	}
	var code structpb.Struct
	if err := st.Details[0].UnmarshalTo(&code); err != nil {
		t.Fatal(err)
	}
	if code.Fields["registry_error_code"].GetStringValue() != "resolution_deferred" {
		t.Fatal(&code)
	}
	var detail RegistryResolutionDetail
	if err := st.Details[1].UnmarshalTo(&detail); err != nil {
		t.Fatal(err)
	}
	var retry errdetails.RetryInfo
	if err := st.Details[2].UnmarshalTo(&retry); err != nil {
		t.Fatal(err)
	}
	if detail.DiscoveryStatus.ConsecutiveFailures != 2 || retry.RetryDelay.Seconds != 45 {
		t.Fatal("incorrect retry details")
	}
}
