package grpc

import (
	"context"

	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/rendau/tiny/internal/handler/grpc/dto"
	usecase "github.com/rendau/tiny/internal/usecase/link"
	"github.com/rendau/tiny/pkg/proto/common"
	proto "github.com/rendau/tiny/pkg/proto/tiny_v1"
)

type Link struct {
	proto.UnsafeLinkServiceServer
	usecase *usecase.Usecase
}

func NewLink(uc *usecase.Usecase) *Link {
	return &Link{usecase: uc}
}

func (h *Link) List(ctx context.Context, req *proto.LinkListReq) (*proto.LinkListRep, error) {
	items, tCount, err := h.usecase.List(ctx, dto.DecodeLinkListReq(req))
	if err != nil {
		return nil, err
	}
	return &proto.LinkListRep{
		PaginationInfo: &common.PaginationInfoSt{
			Page:       req.GetListParams().GetPage(),
			PageSize:   req.GetListParams().GetPageSize(),
			TotalCount: tCount,
		},
		Results: lo.Map(items, dto.EncodeLinkMain),
	}, nil
}

func (h *Link) Get(ctx context.Context, req *proto.LinkGetReq) (*proto.LinkMain, error) {
	item, err := h.usecase.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return dto.EncodeLinkMain(item, 0), nil
}

func (h *Link) Create(ctx context.Context, req *proto.LinkCreateReq) (*proto.LinkCreateRep, error) {
	item, err := h.usecase.Create(ctx, dto.DecodeLinkCreateReq(req))
	if err != nil {
		return nil, err
	}
	return &proto.LinkCreateRep{
		Id:        item.Id,
		ShortCode: item.ShortCode,
		ShortUrl:  item.ShortUrl,
	}, nil
}

func (h *Link) Update(ctx context.Context, req *proto.LinkUpdateReq) (*emptypb.Empty, error) {
	if err := h.usecase.Update(ctx, req.GetId(), dto.DecodeLinkUpdateReq(req)); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (h *Link) Delete(ctx context.Context, req *proto.LinkGetReq) (*emptypb.Empty, error) {
	if err := h.usecase.Delete(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
