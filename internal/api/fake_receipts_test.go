package api

import (
	"context"

	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/store"
)

// fakeReceipts is the package's single stand-in for store.ReceiptStore, so a
// test wires only the calls it is about.
type fakeReceipts struct {
	createReceipt      func(ctx context.Context, in model.ReceiptInput, fields model.ReceiptFields) (model.Receipt, error)
	getReceipt         func(ctx context.Context, id string) (model.Receipt, error)
	listReceipts       func(ctx context.Context, filter store.ReceiptFilter) ([]model.Receipt, int64, error)
	getReceiptByNumber func(ctx context.Context, number int) (model.Receipt, error)
	listRecentReceipts func(ctx context.Context, limit int) ([]model.Receipt, error)
	updateReceipt      func(ctx context.Context, id string, update model.ReceiptUpdate) (model.Receipt, error)
	deleteReceipt      func(ctx context.Context, id string) error
	sumReceipts        func(ctx context.Context, q store.TotalQuery) ([]store.CurrencyTotal, error)
}

var _ store.ReceiptStore = (*fakeReceipts)(nil)

func (f *fakeReceipts) CreateReceipt(ctx context.Context, in model.ReceiptInput, fields model.ReceiptFields) (model.Receipt, error) {
	if f.createReceipt == nil {
		return model.Receipt{}, nil
	}
	return f.createReceipt(ctx, in, fields)
}

func (f *fakeReceipts) GetReceipt(ctx context.Context, id string) (model.Receipt, error) {
	if f.getReceipt == nil {
		return model.Receipt{}, nil
	}
	return f.getReceipt(ctx, id)
}

func (f *fakeReceipts) ListReceipts(ctx context.Context, filter store.ReceiptFilter) ([]model.Receipt, int64, error) {
	if f.listReceipts == nil {
		return nil, 0, nil
	}
	return f.listReceipts(ctx, filter)
}

func (f *fakeReceipts) GetReceiptByNumber(ctx context.Context, number int) (model.Receipt, error) {
	if f.getReceiptByNumber == nil {
		return model.Receipt{}, nil
	}
	return f.getReceiptByNumber(ctx, number)
}

func (f *fakeReceipts) ListRecentReceipts(ctx context.Context, limit int) ([]model.Receipt, error) {
	if f.listRecentReceipts == nil {
		return nil, nil
	}
	return f.listRecentReceipts(ctx, limit)
}

func (f *fakeReceipts) UpdateReceipt(ctx context.Context, id string, update model.ReceiptUpdate) (model.Receipt, error) {
	if f.updateReceipt == nil {
		return model.Receipt{}, nil
	}
	return f.updateReceipt(ctx, id, update)
}

func (f *fakeReceipts) DeleteReceipt(ctx context.Context, id string) error {
	if f.deleteReceipt == nil {
		return nil
	}
	return f.deleteReceipt(ctx, id)
}

func (f *fakeReceipts) SumReceipts(ctx context.Context, q store.TotalQuery) ([]store.CurrencyTotal, error) {
	if f.sumReceipts == nil {
		return nil, nil
	}
	return f.sumReceipts(ctx, q)
}
