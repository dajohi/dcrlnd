package lnrpc

import (
	"errors"

	"github.com/decred/dcrd/dcrutil/v2"
	"github.com/decred/dcrlnd/lnwire"
)

var (
	// ErrAtomsMAtomsMutualExclusive is returned when both an atom and an
	// matom amount are set.
	ErrAtomMAtomMutualExclusive = errors.New(
		"atom and matom arguments are mutually exclusive",
	)
)

// CalculateFeeLimit returns the fee limit in millisatoshis. If a percentage
// based fee limit has been requested, we'll factor in the ratio provided with
// the amount of the payment.
func CalculateFeeLimit(feeLimit *FeeLimit,
	amount lnwire.MilliAtom) lnwire.MilliAtom {

	switch feeLimit.GetLimit().(type) {

	case *FeeLimit_Fixed:
		return lnwire.NewMAtomsFromAtoms(
			dcrutil.Amount(feeLimit.GetFixed()),
		)

	case *FeeLimit_FixedMAtoms:
		return lnwire.MilliAtom(feeLimit.GetFixedMAtoms())

	case *FeeLimit_Percent:
		return amount * lnwire.MilliAtom(feeLimit.GetPercent()) / 100

	default:
		// If a fee limit was not specified, we'll use the payment's
		// amount as an upper bound in order to avoid payment attempts
		// from incurring fees higher than the payment amount itself.
		return amount
	}
}

// UnmarshallAmt returns a strong msat type for a atom/matom pair of rpc
// fields.
func UnmarshallAmt(amtAtom, amtMAtom int64) (lnwire.MilliAtom, error) {
	if amtAtom != 0 && amtMAtom != 0 {
		return 0, ErrAtomMAtomMutualExclusive
	}

	if amtAtom != 0 {
		return lnwire.NewMAtomsFromAtoms(dcrutil.Amount(amtAtom)), nil
	}

	return lnwire.MilliAtom(amtMAtom), nil
}
