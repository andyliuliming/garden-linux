// This file was generated by counterfeiter
package fake_filter_provider

import (
	"sync"

	"github.com/cloudfoundry-incubator/garden-linux/network"
	"github.com/cloudfoundry-incubator/garden-linux/resource_pool"
)

type FakeFilterProvider struct {
	ProvideFilterStub        func(containerId string) network.Filter
	provideFilterMutex       sync.RWMutex
	provideFilterArgsForCall []struct {
		containerId string
	}
	provideFilterReturns struct {
		result1 network.Filter
	}
}

func (fake *FakeFilterProvider) ProvideFilter(containerId string) network.Filter {
	fake.provideFilterMutex.Lock()
	fake.provideFilterArgsForCall = append(fake.provideFilterArgsForCall, struct {
		containerId string
	}{containerId})
	fake.provideFilterMutex.Unlock()
	if fake.ProvideFilterStub != nil {
		return fake.ProvideFilterStub(containerId)
	} else {
		return fake.provideFilterReturns.result1
	}
}

func (fake *FakeFilterProvider) ProvideFilterCallCount() int {
	fake.provideFilterMutex.RLock()
	defer fake.provideFilterMutex.RUnlock()
	return len(fake.provideFilterArgsForCall)
}

func (fake *FakeFilterProvider) ProvideFilterArgsForCall(i int) string {
	fake.provideFilterMutex.RLock()
	defer fake.provideFilterMutex.RUnlock()
	return fake.provideFilterArgsForCall[i].containerId
}

func (fake *FakeFilterProvider) ProvideFilterReturns(result1 network.Filter) {
	fake.ProvideFilterStub = nil
	fake.provideFilterReturns = struct {
		result1 network.Filter
	}{result1}
}

var _ resource_pool.FilterProvider = new(FakeFilterProvider)