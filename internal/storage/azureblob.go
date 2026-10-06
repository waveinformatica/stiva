package storage

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"registry/internal/digest"
)

// AzureBlobStore stores blobs in an Azure Blob Storage container.
type AzureBlobStore struct {
	client    *azblob.Client
	container string
	prefix    string
}

// NewAzureBlobStore creates an Azure-backed BlobStore using a connection string.
func NewAzureBlobStore(cfg BlobConfig) (*AzureBlobStore, error) {
	if cfg.AzureContainer == "" {
		return nil, errors.New("blob: azure requires container")
	}
	if cfg.AzureConnectionString == "" {
		return nil, errors.New("blob: azure requires connection_string")
	}
	client, err := azblob.NewClientFromConnectionString(cfg.AzureConnectionString, nil)
	if err != nil {
		return nil, err
	}
	return &AzureBlobStore{
		client:    client,
		container: cfg.AzureContainer,
		prefix:    strings.Trim(cfg.AzurePrefix, "/"),
	}, nil
}

func (a *AzureBlobStore) key(d digest.Digest) string {
	return objectKey(a.prefix, d.Algorithm().String(), d.Hex())
}

func mapAzureErr(err error) error {
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return ErrNotFound
	}
	return err
}

func (a *AzureBlobStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	resp, err := a.client.DownloadStream(context.Background(), a.container, a.key(d), nil)
	if err != nil {
		return nil, 0, mapAzureErr(err)
	}
	if resp.ContentLength == nil {
		resp.Body.Close()
		return nil, 0, ErrNotFound
	}
	return resp.Body, *resp.ContentLength, nil
}

func (a *AzureBlobStore) Put(d digest.Digest, r io.Reader) error {
	if a.Exists(d) {
		return ErrBlobExists
	}
	h := d.Algorithm().NewHash()
	body := io.TeeReader(r, h)
	if _, err := a.client.UploadStream(context.Background(), a.container, a.key(d), body, nil); err != nil {
		return err
	}
	computed := digest.Digest(d.Algorithm().String() + ":" + digest.EncodeHex(h.Sum(nil)))
	if computed != d {
		_ = a.Delete(d)
		return errors.New("blob: digest mismatch on write")
	}
	return nil
}

func (a *AzureBlobStore) Stat(d digest.Digest) (int64, error) {
	resp, err := a.client.DownloadStream(context.Background(), a.container, a.key(d), nil)
	if err != nil {
		return 0, mapAzureErr(err)
	}
	defer resp.Body.Close()
	if resp.ContentLength == nil {
		return 0, ErrNotFound
	}
	return *resp.ContentLength, nil
}

func (a *AzureBlobStore) Exists(d digest.Digest) bool {
	_, err := a.Stat(d)
	return err == nil
}

func (a *AzureBlobStore) Delete(d digest.Digest) error {
	_, err := a.client.DeleteBlob(context.Background(), a.container, a.key(d), nil)
	if err != nil {
		return mapAzureErr(err)
	}
	return nil
}
