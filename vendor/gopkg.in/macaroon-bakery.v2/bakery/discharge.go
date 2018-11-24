package bakery

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

// LocalThirdPartyCaveat returns a third-party caveat that, when added
// to a macaroon with AddCaveat, results in a caveat
// with the location "local", encrypted with the given public key.
// This can be automatically discharged by DischargeAllWithKey.
func LocalThirdPartyCaveat(key *PublicKey, version Version) checkers.Caveat {
	var loc string
	if version < Version2 {
		loc = "local " + key.String()
	} else {
		loc = fmt.Sprintf("local %d %s", version, key)
	}
	return checkers.Caveat{
		Location: loc,
	}
}

// parseLocalLocation parses a local caveat location as generated by
// LocalThirdPartyCaveat. This is of the form:
//
//	local <version> <pubkey>
//
// where <version> is the bakery version of the client that we're
// adding the local caveat for.
//
// It returns false if the location does not represent a local
// caveat location.
func parseLocalLocation(loc string) (ThirdPartyInfo, bool) {
	if !strings.HasPrefix(loc, "local ") {
		return ThirdPartyInfo{}, false
	}
	version := Version1
	fields := strings.Fields(loc)
	fields = fields[1:] // Skip "local"
	switch len(fields) {
	case 2:
		v, err := strconv.Atoi(fields[0])
		if err != nil {
			return ThirdPartyInfo{}, false
		}
		version = Version(v)
		fields = fields[1:]
		fallthrough
	case 1:
		var key PublicKey
		if err := key.UnmarshalText([]byte(fields[0])); err != nil {
			return ThirdPartyInfo{}, false
		}
		return ThirdPartyInfo{
			PublicKey: key,
			Version:   version,
		}, true
	default:
		return ThirdPartyInfo{}, false
	}
}

// DischargeParams holds parameters for a Discharge call.
type DischargeParams struct {
	// Id holds the id to give to the discharge macaroon.
	// If Caveat is empty, then the id also holds the
	// encrypted third party caveat.
	Id []byte

	// Caveat holds the encrypted third party caveat. If this
	// is nil, Id will be used.
	Caveat []byte

	// Key holds the key to use to decrypt the third party
	// caveat information and to encrypt any additional
	// third party caveats returned by the caveat checker.
	Key *KeyPair

	// Checker is used to check the third party caveat,
	// and may also return further caveats to be added to
	// the discharge macaroon.
	Checker ThirdPartyCaveatChecker

	// Locator is used to information on third parties
	// referred to by third party caveats returned by the Checker.
	Locator ThirdPartyLocator
}

// Discharge creates a macaroon to discharges a third party caveat.
// The given parameters specify the caveat and how it should be checked/
//
// The condition implicit in the caveat is checked for validity using p.Checker. If
// it is valid, a new macaroon is returned which discharges the caveat.
//
// The macaroon is created with a version derived from the version
// that was used to encode the id.
func Discharge(ctx context.Context, p DischargeParams) (*Macaroon, error) {
	var caveatIdPrefix []byte
	if p.Caveat == nil {
		// The caveat information is encoded in the id itself.
		p.Caveat = p.Id
	} else {
		// We've been given an explicit id, so when extra third party
		// caveats are added, use that id as the prefix
		// for any more ids.
		caveatIdPrefix = p.Id
	}
	cavInfo, err := decodeCaveat(p.Key, p.Caveat)
	if err != nil {
		return nil, errgo.Notef(err, "discharger cannot decode caveat id")
	}
	cavInfo.Id = p.Id
	// Note that we don't check the error - we allow the
	// third party checker to see even caveats that we can't
	// understand.
	cond, arg, _ := checkers.ParseCaveat(string(cavInfo.Condition))

	var caveats []checkers.Caveat
	if cond == checkers.CondNeedDeclared {
		cavInfo.Condition = []byte(arg)
		caveats, err = checkNeedDeclared(ctx, cavInfo, p.Checker)
	} else {
		caveats, err = p.Checker.CheckThirdPartyCaveat(ctx, cavInfo)
	}
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	// Note that the discharge macaroon does not need to
	// be stored persistently. Indeed, it would be a problem if
	// we did, because then the macaroon could potentially be used
	// for normal authorization with the third party.
	m, err := NewMacaroon(cavInfo.RootKey, p.Id, "", cavInfo.Version, cavInfo.Namespace)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	m.caveatIdPrefix = caveatIdPrefix
	for _, cav := range caveats {
		if err := m.AddCaveat(ctx, cav, p.Key, p.Locator); err != nil {
			return nil, errgo.Notef(err, "could not add caveat")
		}
	}
	return m, nil
}

func checkNeedDeclared(ctx context.Context, cavInfo *ThirdPartyCaveatInfo, checker ThirdPartyCaveatChecker) ([]checkers.Caveat, error) {
	arg := string(cavInfo.Condition)
	i := strings.Index(arg, " ")
	if i <= 0 {
		return nil, errgo.Newf("need-declared caveat requires an argument, got %q", arg)
	}
	needDeclared := strings.Split(arg[0:i], ",")
	for _, d := range needDeclared {
		if d == "" {
			return nil, errgo.New("need-declared caveat with empty required attribute")
		}
	}
	if len(needDeclared) == 0 {
		return nil, fmt.Errorf("need-declared caveat with no required attributes")
	}
	cavInfo.Condition = []byte(arg[i+1:])
	caveats, err := checker.CheckThirdPartyCaveat(ctx, cavInfo)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	declared := make(map[string]bool)
	for _, cav := range caveats {
		if cav.Location != "" {
			continue
		}
		// Note that we ignore the error. We allow the service to
		// generate caveats that we don't understand here.
		cond, arg, _ := checkers.ParseCaveat(cav.Condition)
		if cond != checkers.CondDeclared {
			continue
		}
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) != 2 {
			return nil, errgo.Newf("declared caveat has no value")
		}
		declared[parts[0]] = true
	}
	// Add empty declarations for everything mentioned in need-declared
	// that was not actually declared.
	for _, d := range needDeclared {
		if !declared[d] {
			caveats = append(caveats, checkers.DeclaredCaveat(d, ""))
		}
	}
	return caveats, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("cannot generate %d random bytes: %v", n, err)
	}
	return b, nil
}

// ThirdPartyCaveatInfo holds the information decoded from
// a third party caveat id.
type ThirdPartyCaveatInfo struct {
	// Condition holds the third party condition to be discharged.
	// This is the only field that most third party dischargers will
	// need to consider.
	Condition []byte

	// FirstPartyPublicKey holds the public key of the party
	// that created the third party caveat.
	FirstPartyPublicKey PublicKey

	// ThirdPartyKeyPair holds the key pair used to decrypt
	// the caveat - the key pair of the discharging service.
	ThirdPartyKeyPair KeyPair

	// RootKey holds the secret root key encoded by the caveat.
	RootKey []byte

	// CaveatId holds the full encoded caveat id from which all
	// the other fields are derived.
	Caveat []byte

	// Version holds the version that was used to encode
	// the caveat id.
	Version Version

	// Id holds the id of the third party caveat (the id that
	// the discharge macaroon should be given). This
	// will differ from Caveat when the caveat information
	// is encoded separately.
	Id []byte

	// Namespace holds the namespace of the first party
	// that created the macaroon, as encoded by the party
	// that added the third party caveat.
	Namespace *checkers.Namespace
}

// ThirdPartyCaveatChecker holds a function that checks third party caveats
// for validity. If the caveat is valid, it returns a nil error and
// optionally a slice of extra caveats that will be added to the
// discharge macaroon. The caveatId parameter holds the still-encoded id
// of the caveat.
//
// If the caveat kind was not recognised, the checker should return an
// error with a ErrCaveatNotRecognized cause.
type ThirdPartyCaveatChecker interface {
	CheckThirdPartyCaveat(ctx context.Context, info *ThirdPartyCaveatInfo) ([]checkers.Caveat, error)
}

// ThirdPartyCaveatCheckerFunc implements ThirdPartyCaveatChecker by calling a function.
type ThirdPartyCaveatCheckerFunc func(context.Context, *ThirdPartyCaveatInfo) ([]checkers.Caveat, error)

// CheckThirdPartyCaveat implements ThirdPartyCaveatChecker.CheckThirdPartyCaveat by calling
// the receiver with the given arguments
func (c ThirdPartyCaveatCheckerFunc) CheckThirdPartyCaveat(ctx context.Context, info *ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	return c(ctx, info)
}

// FirstPartyCaveatChecker is used to check first party caveats
// for validity with respect to information in the provided context.
//
// If the caveat kind was not recognised, the checker should return
// ErrCaveatNotRecognized.
type FirstPartyCaveatChecker interface {
	// CheckFirstPartyCaveat checks that the given caveat condition
	// is valid with respect to the given context information.
	CheckFirstPartyCaveat(ctx context.Context, caveat string) error

	// Namespace returns the namespace associated with the
	// caveat checker.
	Namespace() *checkers.Namespace
}
