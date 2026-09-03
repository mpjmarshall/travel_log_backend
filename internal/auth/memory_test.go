// The exported twin, exercised through the Service seam rather than through
// its own fields.
package auth

import (
	"context"
	"testing"
)

func TestTheTwinCarriesATravellerFromAnInviteToAnAuthenticatedRequest(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	svc := &Service{Store: store}

	invite, hash, err := NewInvite()
	if err != nil {
		t.Fatalf("NewInvite: %v", err)
	}
	if err := store.MintInvite(ctx, hash, "the twin's own leg"); err != nil {
		t.Fatalf("MintInvite: %v", err)
	}

	registered, err := svc.RegisterWithInvite(ctx, "matt@example.com", invite)
	if err != nil {
		t.Fatalf("RegisterWithInvite: %v", err)
	}

	code, _, ok, err := svc.RequestCode(ctx, "matt@example.com")
	if err != nil || !ok {
		t.Fatalf("RequestCode: ok=%v err=%v", ok, err)
	}

	issued, err := svc.SignInWithCode(ctx, "matt@example.com", code)
	if err != nil {
		t.Fatalf("SignInWithCode: %v", err)
	}

	authenticated, err := svc.Authenticate(ctx, issued.Token)
	if err != nil {
		t.Fatalf("Authenticate on the token the twin just issued: %v", err)
	}
	if authenticated.ID != registered.ID {
		t.Errorf("the twin authenticated %q, want the traveller it registered, %q",
			authenticated.ID, registered.ID)
	}
}

func TestTheTwinRefusesATokenItNeverIssued(t *testing.T) {
	svc := &Service{Store: NewMemory()}

	if _, err := svc.Authenticate(context.Background(), "not-a-token-this-twin-minted"); err == nil {
		t.Error("the twin authenticated a token it never issued, so a handler leg " +
			"written against it would prove nothing about a credential")
	}
}
