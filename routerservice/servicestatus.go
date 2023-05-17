package main

import "sync"

type serviceStatus struct {
	listenerOK bool
	sync.RWMutex

	onSuspend   func(string)
	onUnSuspend func()
}

func newServiceStatus() *serviceStatus {
	return &serviceStatus{}
}

func (s *serviceStatus) onAir() bool {
	s.RLock()
	defer s.RUnlock()
	return s.listenerOK
}

func (s *serviceStatus) setListenerStatus(ok bool) {
	s.Lock()
	defer s.Unlock()
	if s.listenerOK == ok {
		return
	}
	if s.listenerOK {
		if s.onSuspend != nil {
			s.onSuspend("listener not ok")
		}
	} else {
		if s.onUnSuspend != nil {
			s.onUnSuspend()
		}
	}
	s.listenerOK = ok
}

func (s *serviceStatus) setOnSuspendFunc(function func(reason string)) {
	s.Lock()
	defer s.Unlock()

	if s.onSuspend == nil {
		s.onSuspend = function
	} else {
		panic("onSuspend func already set")
	}
}

func (s *serviceStatus) setOnUnSuspendFunc(function func()) {
	s.Lock()
	defer s.Unlock()

	if s.onUnSuspend == nil {
		s.onUnSuspend = function
	} else {
		panic("onUnSuspend func already set")
	}
}

// func (s *serviceStatus) isListenerOK() bool {
// 	s.RLock()
// 	defer s.RUnlock()
// 	return s.listenerOK
// }
