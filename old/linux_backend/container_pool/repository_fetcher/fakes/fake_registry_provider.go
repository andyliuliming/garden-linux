// This file was generated by counterfeiter
package fakes

import (
	"sync"

	"github.com/cloudfoundry-incubator/garden-linux/old/linux_backend/container_pool/repository_fetcher"
)

type FakeRegistryProvider struct {
	ProvideRegistryStub        func(hostname string) (repository_fetcher.Registry, error)
	provideRegistryMutex       sync.RWMutex
	provideRegistryArgsForCall []struct {
		hostname string
	}
	provideRegistryReturns struct {
		result1 repository_fetcher.Registry
		result2 error
	}
}

func (fake *FakeRegistryProvider) ProvideRegistry(hostname string) (repository_fetcher.Registry, error) {
	fake.provideRegistryMutex.Lock()
	fake.provideRegistryArgsForCall = append(fake.provideRegistryArgsForCall, struct {
		hostname string
	}{hostname})
	fake.provideRegistryMutex.Unlock()
	if fake.ProvideRegistryStub != nil {
		return fake.ProvideRegistryStub(hostname)
	} else {
		return fake.provideRegistryReturns.result1, fake.provideRegistryReturns.result2
	}
}

func (fake *FakeRegistryProvider) ProvideRegistryCallCount() int {
	fake.provideRegistryMutex.RLock()
	defer fake.provideRegistryMutex.RUnlock()
	return len(fake.provideRegistryArgsForCall)
}

func (fake *FakeRegistryProvider) ProvideRegistryArgsForCall(i int) string {
	fake.provideRegistryMutex.RLock()
	defer fake.provideRegistryMutex.RUnlock()
	return fake.provideRegistryArgsForCall[i].hostname
}

func (fake *FakeRegistryProvider) ProvideRegistryReturns(result1 repository_fetcher.Registry, result2 error) {
	fake.ProvideRegistryStub = nil
	fake.provideRegistryReturns = struct {
		result1 repository_fetcher.Registry
		result2 error
	}{result1, result2}
}

var _ repository_fetcher.RegistryProvider = new(FakeRegistryProvider)
