// Package service provides server side integrations for Kerberos authentication.
package service

import (
	"github.com/jcmturner/gokrb5/types"
	"sync"
	"time"
)

/*The server MUST utilize a replay cache to remember any authenticator
presented within the allowable clock skew.
The replay cache will store at least the server name, along with the
client name, time, and microsecond fields from the recently-seen
authenticators, and if a matching tuple is found, the
KRB_AP_ERR_REPEAT error is returned.  Note that the rejection here is
restricted to authenticators from the same principal to the same
server.  Other client principals communicating with the same server
principal should not have their authenticators rejected if the time
and microsecond fields happen to match some other client's
authenticator.

If a server loses track of authenticators presented within the
allowable clock skew, it MUST reject all requests until the clock
skew interval has passed, providing assurance that any lost or
replayed authenticators will fall outside the allowable clock skew
and can no longer be successfully replayed.  If this were not done,
an attacker could subvert the authentication by recording the ticket
and authenticator sent over the network to a server and replaying
them following an event that caused the server to lose track of
recently seen authenticators.*/

// Cache for tickets received from clients keyed by fully qualified client name. Used to track replay of tickets.
type Cache map[string]clientEntries

// clientEntries holds entries of client details sent to the service.
type clientEntries struct {
	ReplayMap map[time.Time]replayCacheEntry
	SeqNumber int
	SubKey    types.EncryptionKey
}

// Cache entry tracking client time values of tickets sent to the service.
type replayCacheEntry struct {
	PresentedTime time.Time
	SName         types.PrincipalName
	CTime         time.Time // This combines the ticket's CTime and Cusec
}

// Instance of the ServiceCache. This needs to be a singleton.
var replayCache Cache
var once sync.Once

// GetReplayCache returns a pointer to the Cache singleton.
func GetReplayCache(d time.Duration) *Cache {
	// Create a singleton of the ReplayCache and start a background thread to regularly clean out old entries
	once.Do(func() {
		replayCache = make(Cache)
		go func() {
			for {
				// TODO consider using a context here.
				time.Sleep(d)
				replayCache.ClearOldEntries(d)
			}
		}()
	})
	return &replayCache
}

// AddEntry adds an entry to the Cache.
func (c *Cache) AddEntry(sname types.PrincipalName, a types.Authenticator) {
	ct := a.CTime.Add(time.Duration(a.Cusec) * time.Microsecond)
	if ce, ok := (*c)[a.CName.GetPrincipalNameString()]; ok {
		ce.ReplayMap[ct] = replayCacheEntry{
			PresentedTime: time.Now().UTC(),
			SName:         sname,
			CTime:         ct,
		}
		ce.SeqNumber = a.SeqNumber
		ce.SubKey = a.SubKey
	} else {
		(*c)[a.CName.GetPrincipalNameString()] = clientEntries{
			ReplayMap: map[time.Time]replayCacheEntry{
				ct: {
					PresentedTime: time.Now().UTC(),
					SName:         sname,
					CTime:         ct,
				},
			},
			SeqNumber: a.SeqNumber,
			SubKey:    a.SubKey,
		}
	}
}

// ClearOldEntries clears entries from the Cache that are older than the duration provided.
func (c *Cache) ClearOldEntries(d time.Duration) {
	for ck := range *c {
		for ct, e := range (*c)[ck].ReplayMap {
			if time.Now().UTC().Sub(e.PresentedTime) > d {
				delete((*c)[ck].ReplayMap, ct)
			}
		}
		if len((*c)[ck].ReplayMap) == 0 {
			delete((*c), ck)
		}
	}
}

// IsReplay tests if the Authenticator provided is a replay within the duration defined. If this is not a replay add the entry to the cache for tracking.
func (c *Cache) IsReplay(sname types.PrincipalName, a types.Authenticator) bool {
	if ck, ok := (*c)[a.CName.GetPrincipalNameString()]; ok {
		ct := a.CTime.Add(time.Duration(a.Cusec) * time.Microsecond)
		if e, ok := ck.ReplayMap[ct]; ok {
			if e.SName.Equal(sname) {
				return true
			}
		}
	}
	c.AddEntry(sname, a)
	return false
}
