package logic

import (
	"encoding/json"

	"github.com/boltdb/bolt"
	"github.com/golang/protobuf/proto"
	core "github.com/jtremback/usc/core/peer"
	"github.com/jtremback/usc/core/wire"
	"github.com/jtremback/usc/peer/access"
)

type CallerAPI struct {
	DB                 *bolt.DB
	CounterpartyClient CounterpartyClient
	JudgeClient        JudgeClient
}

type JudgeClient interface {
	GetFinalUpdateTx(string) (*wire.Envelope, error)
	AddChannel(*wire.Envelope, string) error
	AddCancellationTx(*wire.Envelope, string) error
	AddUpdateTx(*wire.Envelope, string) error
	AddFollowOnTx(*wire.Envelope, string) error
	GetChannel(string, string) ([]byte, error)
}

type CounterpartyClient interface {
	AddChannel(*wire.Envelope, string) error
	AddUpdateTx(*wire.Envelope, string) error
	AddFullUpdateTx(*wire.Envelope, string) error
}

func (a *CallerAPI) NewAccount(
	name string,
	judge []byte,
) (*core.Account, error) {
	var err error
	acct := &core.Account{}
	err = a.DB.Update(func(tx *bolt.Tx) error {
		jd, err := access.GetJudge(tx, judge)
		if err != nil {
			return err
		}

		acct, err = core.NewAccount(name, jd)
		if err != nil {
			return err
		}
		err = access.SetAccount(tx, acct)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return acct, nil
}

func (a *CallerAPI) AddAccount(
	name string,
	judge []byte,
	pubkey []byte,
	privkey []byte,
) error {
	return a.DB.Update(func(tx *bolt.Tx) error {
		jd, err := access.GetJudge(tx, judge)
		if err != nil {
			return err
		}

		acct := &core.Account{
			Name:    name,
			Judge:   jd,
			Pubkey:  pubkey,
			Privkey: privkey,
		}

		err = access.SetAccount(tx, acct)
		if err != nil {
			return err
		}

		return nil
	})
}

func (a *CallerAPI) AddCounterparty(
	name string,
	judge []byte,
	pubkey []byte,
	address string,
) error {
	return a.DB.Update(func(tx *bolt.Tx) error {
		jd, err := access.GetJudge(tx, judge)
		if err != nil {
			return err
		}

		cpt := &core.Counterparty{
			Name:    name,
			Judge:   jd,
			Pubkey:  pubkey,
			Address: address,
		}

		err = access.SetCounterparty(tx, cpt)
		if err != nil {
			return err
		}

		return nil
	})
}

func (a *CallerAPI) AddJudge(
	name string,
	pubkey []byte,
	address string,
) error {
	return a.DB.Update(func(tx *bolt.Tx) error {
		jd := &core.Judge{
			Name:    name,
			Pubkey:  pubkey,
			Address: address,
		}

		err := access.SetJudge(tx, jd)
		if err != nil {
			return err
		}

		return nil
	})
}

func (a *CallerAPI) ViewChannels() ([]*core.Channel, error) {
	var chs []*core.Channel
	var err error
	err = a.DB.View(func(tx *bolt.Tx) error {
		chs, err = access.GetChannels(tx)

		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return chs, nil
}

// func (a *CallerAPI) CheckChannels() ([]*core.Channel, error) {
// 	var chs []*core.Channel
// 	var err error
// 	err = a.DB.Update(func(tx *bolt.Tx) error {
// 		accts, err = access.GetAccounts(tx)
// 		if err != nil {
// 			return err
// 		}

// 		for _, acct := range accts {
// 			a.JudgeClient.GetChannels()
// 		}

// 		return nil
// 	})
// 	if err != nil {
// 		return nil, err
// 	}

// 	return chs, nil
// }

// ProposeChannel is called to propose a new channel. It creates and signs an
// OpeningTx, sends it to the Counterparty and saves it in a new Channel.
func (a *CallerAPI) ProposeChannel(
	channelId string,
	state []byte,
	myPubkey []byte,
	theirPubkey []byte,
	holdPeriod uint32,
) (*core.Channel, error) {
	ch := &core.Channel{}
	err := a.DB.Update(func(tx *bolt.Tx) error {
		acct, err := access.GetAccount(tx, myPubkey)
		if err != nil {
			return err
		}

		cpt, err := access.GetCounterparty(tx, theirPubkey)
		if err != nil {
			return err
		}

		otx, err := acct.NewOpeningTx(channelId, cpt, state, holdPeriod)
		if err != nil {
			return err
		}

		ev, err := core.SerializeOpeningTx(otx)
		if err != nil {
			return err
		}

		acct.AppendSignature(ev)

		ch, err = core.NewChannel(ev, otx, acct, cpt)
		if err != nil {
			return err
		}

		err = a.CounterpartyClient.AddChannel(ev, cpt.Address)
		if err != nil {
			return err
		}

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return ch, nil
}

// AcceptChannel is called on Channels which are in phase PENDING_OPEN. It signs
// the Channel's OpeningTx and sends it to the Judge.
func (a *CallerAPI) AcceptChannel(channelID string) error {
	var err error
	return a.DB.Update(func(tx *bolt.Tx) error {
		var ch *core.Channel
		ch, err = access.GetChannel(tx, channelID)
		if err != nil {
			return err
		}

		ch.Account.AppendSignature(ch.OpeningTxEnvelope)

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		err = a.JudgeClient.AddChannel(ch.OpeningTxEnvelope, ch.Judge.Address)
		if err != nil {
			return err
		}

		return nil
	})
}

// This gets the channel from the judge and checks if it has changed, and does stuff if it has
func (a *CallerAPI) CheckChannel(chId string) error {
	return a.DB.Update(func(tx *bolt.Tx) error {
		ch, err := access.GetChannel(tx, chId)
		if err != nil {
			return err
		}

		b, err := a.JudgeClient.GetChannel(chId, ch.Judge.Address)
		if err != nil {
			return err
		}

		jch := &core.Channel{}
		json.Unmarshal(b, jch)

		// This means that the judge has signed the channel
		if ch.Phase == core.PENDING_OPEN && jch.Phase == core.OPEN {
			ch.Open(jch.OpeningTxEnvelope, jch.OpeningTx)
			if err != nil {
				return err
			}
		}

		// // ch.OpeningTx = jch.OpeningTx
		// // ch.OpeningTxEnvelope = jch.OpeningTxEnvelope
		// // ch.LastFullUpdateTx = jch.LastFullUpdateTx
		// // ch.LastFullUpdateTxEnvelope = jch.LastFullUpdateTxEnvelope

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		return nil
	})
}

// // OpenChannel is called on Channels which are in phase PENDING_OPEN. It checks
// // an OpeningTx signed by the Judge, and if everything is correct puts the Channel
// // into phase OPEN.
// func (a *CallerAPI) OpenChannel(ev *wire.Envelope) error {
// 	var err error
// 	return a.DB.Update(func(tx *bolt.Tx) error {
// 		ch := &core.Channel{}
// 		otx := &wire.OpeningTx{}
// 		err = proto.Unmarshal(ev.Payload, otx)
// 		if err != nil {
// 			return err
// 		}

// 		ch, err = access.GetChannel(tx, otx.ChannelId)
// 		if err != nil {
// 			return err
// 		}

// 		ch.Open(ev, otx)
// 		if err != nil {
// 			return err
// 		}

// 		err = access.SetChannel(tx, ch)
// 		if err != nil {
// 			return err
// 		}

// 		return nil
// 	})
// }

// NewUpdateTx is called on Channels which are in phase OPEN. It makes a new UpdateTx,
// signs it, saves it as MyProposedUpdateTx, and sends it to the Counterparty.
func (a *CallerAPI) NewUpdateTx(state []byte, channelID string, fast bool) error {
	var err error
	return a.DB.Update(func(tx *bolt.Tx) error {
		ch := &core.Channel{}
		ch, err = access.GetChannel(tx, channelID)
		if err != nil {
			return err
		}

		utx := ch.NewUpdateTx(state, fast)

		ev, err := core.SerializeUpdateTx(utx)
		if err != nil {
			return err
		}

		ch.SignProposedUpdateTx(ev, utx)

		err = a.CounterpartyClient.AddUpdateTx(ev, ch.Counterparty.Address)
		if err != nil {
			return err
		}

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		return nil
	})
}

// CosignUpdateTx cosigns the Channel's TheirProposedUpdateTx, saves it to
// LastFullUpdateTx, and sends it to the Counterparty.
func (a *CallerAPI) CosignUpdateTx(channelID string) error {
	return a.DB.Update(func(tx *bolt.Tx) error {
		ch, err := access.GetChannel(tx, channelID)
		if err != nil {
			return err
		}

		ev := ch.CosignProposedUpdateTx()

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		err = a.CounterpartyClient.AddFullUpdateTx(ev, ch.Counterparty.Address)
		if err != nil {
			return err
		}

		return nil
	})
}

// CheckFinalUpdateTx checks with the Judge to see if the Counterparty has posted
// an UpdateTx. If the UpdateTx from the Judge has a lower SequenceNumber than
// LastFullUpdateTx, we send LastFullUpdateTx to the Judge.
func (a *CallerAPI) CheckFinalUpdateTx(channelID string) error {
	// var err error
	return a.DB.Update(func(tx *bolt.Tx) error {
		ch, err := access.GetChannel(tx, channelID)
		if err != nil {
			return err
		}

		ev, err := a.JudgeClient.GetFinalUpdateTx(ch.Judge.Address)
		if err != nil {
			return err
		}

		utx := &wire.UpdateTx{}
		err = proto.Unmarshal(ev.Payload, utx)
		if err != nil {
			return err
		}

		newerUpdateTx, err := ch.AddFinalUpdateTx(ev, utx)
		if err != nil {
			return err
		}

		if newerUpdateTx != nil {
			err = a.JudgeClient.AddUpdateTx(newerUpdateTx, ch.Judge.Address)
			if err != nil {
				return err
			}
		}

		err = access.SetChannel(tx, ch)
		if err != nil {
			return err
		}

		return nil
	})
}
