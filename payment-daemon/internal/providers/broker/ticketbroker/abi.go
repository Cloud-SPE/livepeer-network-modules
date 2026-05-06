package ticketbroker

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

// ticketBrokerABI is a minimal JSON subset of the on-chain TicketBroker
// ABI covering only the four methods the daemon calls. Adapted from
// go-livepeer's eth/contracts/ticketBroker.go (MIT) with all other
// functions and events stripped.
const ticketBrokerABI = `[
  {"type":"function","name":"getSenderInfo","stateMutability":"view",
   "inputs":[{"name":"_sender","type":"address"}],
   "outputs":[
     {"components":[{"name":"deposit","type":"uint256"},{"name":"withdrawRound","type":"uint256"}],"name":"sender","type":"tuple"},
     {"components":[{"name":"fundsRemaining","type":"uint256"},{"name":"claimedInCurrentRound","type":"uint256"}],"name":"reserve","type":"tuple"}
   ]},
  {"type":"function","name":"claimedReserve","stateMutability":"view",
   "inputs":[{"name":"_reserveHolder","type":"address"},{"name":"_claimant","type":"address"}],
   "outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"usedTickets","stateMutability":"view",
   "inputs":[{"name":"","type":"bytes32"}],
   "outputs":[{"name":"","type":"bool"}]},
  {"type":"function","name":"redeemWinningTicket","stateMutability":"nonpayable",
   "inputs":[
     {"components":[
       {"name":"recipient","type":"address"},
       {"name":"sender","type":"address"},
       {"name":"faceValue","type":"uint256"},
       {"name":"winProb","type":"uint256"},
       {"name":"senderNonce","type":"uint256"},
       {"name":"recipientRandHash","type":"bytes32"},
       {"name":"auxData","type":"bytes"}
     ],"name":"_ticket","type":"tuple"},
     {"name":"_sig","type":"bytes"},
     {"name":"_recipientRand","type":"uint256"}
   ],
   "outputs":[]}
]`

// ParsedABI is the pre-parsed TicketBroker ABI. Package-level so callers
// that need to encode call data directly (e.g. tests) share one parse.
var ParsedABI = mustParseABI(ticketBrokerABI)

func mustParseABI(src string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic("ticketbroker: malformed ABI: " + err.Error())
	}
	return a
}

// solTicket is the Go shape of MTicketBrokerCore.Ticket — the tuple the
// contract's redeemWinningTicket takes. Field order and capitalization
// must match the ABI component names so abi.Pack resolves them.
type solTicket struct {
	Recipient         ethcommon.Address `abi:"recipient"`
	Sender            ethcommon.Address `abi:"sender"`
	FaceValue         *big.Int          `abi:"faceValue"`
	WinProb           *big.Int          `abi:"winProb"`
	SenderNonce       *big.Int          `abi:"senderNonce"`
	RecipientRandHash [32]byte          `abi:"recipientRandHash"`
	AuxData           []byte            `abi:"auxData"`
}

type solSender struct {
	Deposit       *big.Int `abi:"deposit"`
	WithdrawRound *big.Int `abi:"withdrawRound"`
}

type solReserve struct {
	FundsRemaining        *big.Int `abi:"fundsRemaining"`
	ClaimedInCurrentRound *big.Int `abi:"claimedInCurrentRound"`
}

type senderInfoResult struct {
	Sender  solSender  `abi:"sender"`
	Reserve solReserve `abi:"reserve"`
}
