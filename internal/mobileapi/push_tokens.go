package mobileapi

import "mobile_server/internal/core"

type PushTokenStore = core.PushTokenStore

func NewPushTokenStore(path string) *PushTokenStore {
	return core.NewPushTokenStore(path)
}
