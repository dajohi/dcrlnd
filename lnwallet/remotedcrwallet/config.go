package remotedcrwallet

import (
	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/dcrlnd/channeldb"
	"github.com/decred/dcrlnd/lnwallet"
	"github.com/decred/dcrlnd/lnwallet/chainfee"
	"google.golang.org/grpc"
)

// WalletSyncer is an exported interface for the available wallet sync backends
// (RPC, SPV, etc). While this interface is exported to ease construction of a
// Config structure, only implementations provided by this package are
// supported, since currently the implementation is tightly coupled to the
// DcrWallet struct.
//
// The current backend implementations also implement the BlockChainIO
// interface.
type WalletSyncer interface {
	lnwallet.BlockChainIO

	start(w *DcrWallet) error
	stop()
}

// Config is a struct which houses configuration parameters which modify the
// instance of DcrWallet generated by the New() function.
type Config struct {
	// PrivatePass is the private password to the underlying dcrwallet
	// instance. Without this, the wallet cannot be decrypted and operated.
	PrivatePass []byte

	// FeeEstimator is an instance of the fee estimator interface which
	// will be used by the wallet to dynamically set transaction fees when
	// crafting transactions.
	FeeEstimator chainfee.Estimator

	// NetParams is the net parameters for the target chain.
	NetParams *chaincfg.Params

	// Conn is a grpc connection to an already opened wallet. This needs to
	// be filled for the wallet to work.
	Conn *grpc.ClientConn

	// AccountNumber is the account number to use to perform the ln
	// operations.
	AccountNumber int32

	// ChainIO is a direct connection to the blockchain IO driver needed by
	// the wallet.
	//
	// TODO(decred) Ideally this should be performed by wallet operations
	// but not all operations needed by the drivers are currently
	// implemented in the wallet.
	ChainIO lnwallet.BlockChainIO

	DB *channeldb.DB
}
