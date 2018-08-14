package bolt

import (
	"bytes"
	"context"
	"encoding/json"

	bolt "github.com/coreos/bbolt"
	"github.com/influxdata/platform"
)

var (
	cellBucket = []byte("cellsv2")
)

func (c *Client) initializeViews(ctx context.Context, tx *bolt.Tx) error {
	if _, err := tx.CreateBucketIfNotExists([]byte(cellBucket)); err != nil {
		return err
	}
	return nil
}

// FindViewByID retrieves a cell by id.
func (c *Client) FindViewByID(ctx context.Context, id platform.ID) (*platform.View, error) {
	var d *platform.View

	err := c.db.View(func(tx *bolt.Tx) error {
		dash, err := c.findViewByID(ctx, tx, id)
		if err != nil {
			return err
		}
		d = dash
		return nil
	})

	if err != nil {
		return nil, err
	}

	return d, nil
}

func (c *Client) findViewByID(ctx context.Context, tx *bolt.Tx, id platform.ID) (*platform.View, error) {
	var d platform.View

	v := tx.Bucket(cellBucket).Get([]byte(id))

	if len(v) == 0 {
		return nil, platform.ErrViewNotFound
	}

	if err := json.Unmarshal(v, &d); err != nil {
		return nil, err
	}

	return &d, nil
}

// FindView retrieves a cell using an arbitrary cell filter.
func (c *Client) FindView(ctx context.Context, filter platform.ViewFilter) (*platform.View, error) {
	if filter.ID != nil {
		return c.FindViewByID(ctx, *filter.ID)
	}

	var d *platform.View
	err := c.db.View(func(tx *bolt.Tx) error {
		filterFn := filterViewsFn(filter)
		return c.forEachView(ctx, tx, func(dash *platform.View) bool {
			if filterFn(dash) {
				d = dash
				return false
			}
			return true
		})
	})

	if err != nil {
		return nil, err
	}

	if d == nil {
		return nil, platform.ErrViewNotFound
	}

	return d, nil
}

func filterViewsFn(filter platform.ViewFilter) func(d *platform.View) bool {
	if filter.ID != nil {
		return func(d *platform.View) bool {
			return bytes.Equal(d.ID, *filter.ID)
		}
	}

	return func(d *platform.View) bool { return true }
}

// FindViews retrives all cells that match an arbitrary cell filter.
func (c *Client) FindViews(ctx context.Context, filter platform.ViewFilter) ([]*platform.View, int, error) {
	if filter.ID != nil {
		d, err := c.FindViewByID(ctx, *filter.ID)
		if err != nil {
			return nil, 0, err
		}

		return []*platform.View{d}, 1, nil
	}

	ds := []*platform.View{}
	err := c.db.View(func(tx *bolt.Tx) error {
		dashs, err := c.findViews(ctx, tx, filter)
		if err != nil {
			return err
		}
		ds = dashs
		return nil
	})

	if err != nil {
		return nil, 0, err
	}

	return ds, len(ds), nil
}

func (c *Client) findViews(ctx context.Context, tx *bolt.Tx, filter platform.ViewFilter) ([]*platform.View, error) {
	ds := []*platform.View{}

	filterFn := filterViewsFn(filter)
	err := c.forEachView(ctx, tx, func(d *platform.View) bool {
		if filterFn(d) {
			ds = append(ds, d)
		}
		return true
	})

	if err != nil {
		return nil, err
	}

	return ds, nil
}

// CreateView creates a platform cell and sets d.ID.
func (c *Client) CreateView(ctx context.Context, d *platform.View) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		d.ID = c.IDGenerator.ID()
		return c.putView(ctx, tx, d)
	})
}

// PutView will put a cell without setting an ID.
func (c *Client) PutView(ctx context.Context, d *platform.View) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		return c.putView(ctx, tx, d)
	})
}

func (c *Client) putView(ctx context.Context, tx *bolt.Tx, d *platform.View) error {
	v, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := tx.Bucket(cellBucket).Put([]byte(d.ID), v); err != nil {
		return err
	}
	return nil
}

// forEachView will iterate through all cells while fn returns true.
func (c *Client) forEachView(ctx context.Context, tx *bolt.Tx, fn func(*platform.View) bool) error {
	cur := tx.Bucket(cellBucket).Cursor()
	for k, v := cur.First(); k != nil; k, v = cur.Next() {
		d := &platform.View{}
		if err := json.Unmarshal(v, d); err != nil {
			return err
		}
		if !fn(d) {
			break
		}
	}

	return nil
}

// UpdateView updates a cell according the parameters set on upd.
func (c *Client) UpdateView(ctx context.Context, id platform.ID, upd platform.ViewUpdate) (*platform.View, error) {
	var d *platform.View
	err := c.db.Update(func(tx *bolt.Tx) error {
		dash, err := c.updateView(ctx, tx, id, upd)
		if err != nil {
			return err
		}
		d = dash
		return nil
	})

	return d, err
}

func (c *Client) updateView(ctx context.Context, tx *bolt.Tx, id platform.ID, upd platform.ViewUpdate) (*platform.View, error) {
	d, err := c.findViewByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	if upd.Name != nil {
		d.Name = *upd.Name
	}

	if upd.Properties != nil {
		d.Properties = upd.Properties
	}

	if err := c.putView(ctx, tx, d); err != nil {
		return nil, err
	}

	return d, nil
}

// DeleteView deletes a cell and prunes it from the index.
func (c *Client) DeleteView(ctx context.Context, id platform.ID) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		return c.deleteView(ctx, tx, id)
	})
}

func (c *Client) deleteView(ctx context.Context, tx *bolt.Tx, id platform.ID) error {
	_, err := c.findViewByID(ctx, tx, id)
	if err != nil {
		return err
	}
	return tx.Bucket(cellBucket).Delete([]byte(id))
}