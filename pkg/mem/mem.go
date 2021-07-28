package mem

import (
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/ulule/deepcopier"
)

// Data define the type which can speed up by mem cache
type Data interface {
	Fulfill(key string) (err error)
}

// Cache is the definition of cache, be careful of the memory usage
type Cache struct {
	old     *sync.Map
	now     *sync.Map
	barrier *sync.Map
	rotate  <-chan time.Time
	rwmutex *sync.RWMutex
}

// DefaultCache is default cache for surge
var DefaultCache = New(time.Minute)

// Remember is a surge, it provides a quite simple way to use cache
func Remember(dst Data, key string) error {
	return DefaultCache.Remember(dst, key)
}

// Delete is a surge, it delete a specified data in DefaultCache
func Delete(dst Data, key string) {
	DefaultCache.Delete(dst, key)
}

// New create a cache entity with a custom expiration time
func New(rotateInterval time.Duration) *Cache {
	return &Cache{
		old:     &sync.Map{},
		now:     &sync.Map{},
		barrier: &sync.Map{},
		rotate:  time.NewTicker(rotateInterval).C,
		rwmutex: &sync.RWMutex{},
	}
}

// Remember automatically save and retrieve data from a cache entity
func (c *Cache) Remember(dst Data, key string) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr {
		panic("invalid not pointor type: " + reflect.TypeOf(dst).Name())
	} else if rv.IsNil() {
		return errors.New("invalid nil pointor")
	}

	c.rwmutex.RLock()
	defer c.rwmutex.RUnlock()

	// rotate logic, rwlock just protect fields in Cache, but not field content.
	// So that, write lock just take a very short time, and simple read lock is
	// just an atomic action, do not care the performance
	select {
	case <-c.rotate:
		c.old = c.now
		c.now = &sync.Map{}
		c.barrier = &sync.Map{}
	default:
	}

	// First: load from cache
	if val, ok := c.now.Load(key); ok {
		return deepcopier.Copy(val).To(dst)
	}

	// Second: load from old cache, or waitting the sigle groutine getting data
	ch := make(chan struct{})
	if chVal, ok := c.barrier.LoadOrStore(key, ch); ok {
		close(ch) // the ch is not used

		if val, ok := c.old.Load(key); ok {
			return deepcopier.Copy(val).To(dst)
		}

		// type chan:  wait the sigle groutine getting data
		// type error: already failed
		if ch, ok = chVal.(chan struct{}); ok {
			<-ch
			if val, ok := c.now.Load(key); ok {
				return deepcopier.Copy(val).To(dst)
			}
		}

		val, _ := c.barrier.Load(key)
		if err, ok := val.(error); ok {
			return err
		}

		panic("new value lost, please report a bug")
	}

	// Third: getting data from CacheType, maybe from db
	err := dst.Fulfill(key)
	if err != nil {
		c.barrier.Store(key, err)
		return err
	}

	c.now.Store(key, dst)
	close(ch) // broadcast, wakeup all waiting groutine

	return nil
}

// Delete immediately specified the cached content to expire
func (c *Cache) Delete(dst Data, key string) {
	c.rwmutex.Lock()
	defer c.rwmutex.Unlock()

	c.old.Delete(key)
	c.now.Delete(key)
	c.barrier.Store(key, errors.New(key+" is deleted"))
}

// Rotate force refresh cached data
func (c *Cache) Rotate(reset bool) {
	c.rwmutex.Lock()
	defer c.rwmutex.Unlock()

	if reset {
		c.old = &sync.Map{}
	} else {
		c.old = c.now
	}
	c.now = &sync.Map{}
	c.barrier = &sync.Map{}
}
