package cache

import "container/list"

// // // // // // // // // //

func (s *shardObj) get(key string, nowNano int64) (value EntryObj, found bool, freed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	elem, ok := s.itemMap[key]
	if !ok {
		return EntryObj{}, false, 0
	}
	itemObj := elem.Value.(*cacheItemObj)
	if itemObj.expiry <= nowNano {
		s.lru.Remove(elem)
		delete(s.itemMap, key)
		s.entries.Add(-1)
		s.bytes.Add(-itemObj.size)
		return EntryObj{}, false, itemObj.size
	}
	s.lru.MoveToFront(elem)
	return itemObj.value, true, 0
}

func (s *shardObj) insert(itemObj *cacheItemObj) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	delta := itemObj.size
	if existing, ok := s.itemMap[itemObj.key]; ok {
		delta -= existing.Value.(*cacheItemObj).size
		s.lru.Remove(existing)
	} else {
		s.entries.Add(1)
	}
	s.itemMap[itemObj.key] = s.lru.PushFront(itemObj)
	s.bytes.Add(delta)
	return delta
}

func (s *shardObj) evictTail() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	tail := s.lru.Back()
	if tail == nil {
		return 0
	}
	itemObj := tail.Value.(*cacheItemObj)
	s.lru.Remove(tail)
	delete(s.itemMap, itemObj.key)
	s.entries.Add(-1)
	s.bytes.Add(-itemObj.size)
	return itemObj.size
}

func (s *shardObj) clear() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	freed := s.bytes.Load()
	s.itemMap = make(map[string]*list.Element)
	s.lru.Init()
	s.entries.Store(0)
	s.bytes.Store(0)
	return freed
}

func (s *shardObj) stats() (entries int, bytes int64) {
	return int(s.entries.Load()), s.bytes.Load()
}
