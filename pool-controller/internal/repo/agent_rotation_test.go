package repo

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestAgentRotationExactRecoveryAndGenerationFence(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	oldToken := "old-token"
	host := types.HostEnrollment{ID: "host", PoolID: store.PoolID(), EnrollmentTokenHash: rotationHash(oldToken), BrokerSessionCredential: "old-attach", CredentialGeneration: 1, Status: types.HostEnrollmentActive}
	if err := store.PutHostEnrollment(host); err != nil {
		t.Fatal(err)
	}
	proof := strings.Repeat("a", 64)
	pair, err := store.RotateAgentCredentials("host", oldToken, proof)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Generation != 2 || pair.Token == oldToken || pair.AttachCredential == host.BrokerSessionCredential {
		t.Fatal("secrets not rotated")
	}
	store.Close()
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.RotateAgentCredentials("host", oldToken, proof)
	if err != nil || again != pair {
		t.Fatal("lost-response/restart changed result", err)
	}
	for _, attempt := range [][2]string{{oldToken, strings.Repeat("b", 64)}, {"wrong", proof}, {pair.Token, proof}} {
		if _, err := store.RotateAgentCredentials("host", attempt[0], attempt[1]); err == nil {
			t.Fatal("arbitrary revoked-token recovery", attempt[0] == oldToken)
		}
	}
	if err := store.AckAgentRotation("host", oldToken, proof); err == nil {
		t.Fatal("old token acknowledged new credentials")
	}
	if err := store.AckAgentRotation("host", pair.Token, proof); err != nil {
		t.Fatal(err)
	}
	if err := store.AckAgentRotation("host", pair.Token, proof); err != nil {
		t.Fatal("ack replay", err)
	}
	if _, err := store.RotateAgentCredentials("host", oldToken, proof); err == nil {
		t.Fatal("acknowledged result still retrievable")
	}
	nextProof := strings.Repeat("c", 64)
	next, err := store.RotateAgentCredentials("host", pair.Token, nextProof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateEnrollmentCredentials("host", rotationHash(next.Token), rotationHash("member-replacement"), "member-attach", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateAgentCredentials("host", pair.Token, nextProof); err == nil {
		t.Fatal("manual rotation failed to revoke pending agent recovery")
	}
	current, _ := store.GetHostEnrollment("host")
	current.Status = types.HostEnrollmentRetired
	if err := store.PutHostEnrollment(current); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateAgentCredentials("host", "member-replacement", strings.Repeat("d", 64)); err == nil {
		t.Fatal("retired enrollment revived")
	}
}
