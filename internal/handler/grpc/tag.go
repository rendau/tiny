package grpc

import (
	"context"

	usecase "github.com/rendau/tiny/internal/usecase/link"
	proto "github.com/rendau/tiny/pkg/proto/tiny_v1"
)

// Tag — метки ссылок: отдельной сущности нет, метки берутся из самих ссылок
type Tag struct {
	proto.UnsafeTagServiceServer
	usecase *usecase.Usecase
}

func NewTag(uc *usecase.Usecase) *Tag {
	return &Tag{usecase: uc}
}

func (h *Tag) List(ctx context.Context, req *proto.TagListReq) (*proto.TagListRep, error) {
	tags, err := h.usecase.TagList(ctx, req.Search)
	if err != nil {
		return nil, err
	}
	return &proto.TagListRep{Results: tags}, nil
}
