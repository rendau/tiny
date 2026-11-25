package dto

import (
	"time"

	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/timestamppb"

	domainModel "github.com/rendau/tiny/internal/domain/link/model"
	proto "github.com/rendau/tiny/pkg/proto/tiny_v1"
)

// domain → proto

func EncodeLinkMain(v *domainModel.Main, _ int) *proto.LinkMain {
	if v == nil {
		return nil
	}
	return &proto.LinkMain{
		Id:           v.Id,
		CreatedAt:    timestamppb.New(v.CreatedAt),
		ModifiedAt:   timestamppb.New(v.ModifiedAt),
		Domain:       v.Domain,
		ShortCode:    v.ShortCode,
		ShortUrl:     v.ShortUrl,
		LongUrl:      v.LongUrl,
		Title:        v.Title,
		Tags:         v.Tags,
		ValidSince:   encodeLinkTime(v.ValidSince),
		ValidUntil:   encodeLinkTime(v.ValidUntil),
		MaxVisits:    v.MaxVisits,
		Crawlable:    v.Crawlable,
		ForwardQuery: v.ForwardQuery,
	}
}

// encodeLinkTime — необязательное время: nil остаётся незаданным полем
func encodeLinkTime(v *time.Time) *timestamppb.Timestamp {
	if v == nil {
		return nil
	}
	return timestamppb.New(*v)
}

// proto → domain

func DecodeLinkListReq(v *proto.LinkListReq) *domainModel.ListReq {
	return &domainModel.ListReq{
		ListParams: DecodeListParams(v.GetListParams()),
		Search:     v.Search,
		ShortCode:  v.ShortCode,
		Tag:        v.Tag,
		Expired:    v.Expired,
		Domain:     v.Domain,
	}
}

func DecodeLinkCreateReq(v *proto.LinkCreateReq) *domainModel.CreateReq {
	return &domainModel.CreateReq{
		LongUrl: v.GetLongUrl(),
		// пустой код — сгенерировать, пустой домен — основной
		ShortCode:       lo.EmptyableToPtr(v.GetShortCode()),
		ShortCodeLength: decodeLinkCodeLength(v.ShortCodeLength),
		Domain:          lo.EmptyableToPtr(v.GetDomain()),
		Title:           v.Title,
		Tags:            v.GetTags(),
		ValidSince:      decodeLinkTime(v.GetValidSince()),
		ValidUntil:      decodeLinkTime(v.GetValidUntil()),
		MaxVisits:       v.MaxVisits,
		Crawlable:       v.Crawlable,
		ForwardQuery:    v.ForwardQuery,
	}
}

// DecodeLinkUpdateReq — правка заменяет все изменяемые поля: незаданные срок и предел переходов
// снимаются
func DecodeLinkUpdateReq(v *proto.LinkUpdateReq) *domainModel.Edit {
	tags := v.GetTags()
	if tags == nil {
		tags = []string{}
	}

	return &domainModel.Edit{
		LongUrl:      new(v.GetLongUrl()),
		Title:        new(v.GetTitle()),
		Tags:         &tags,
		ValidSince:   new(decodeLinkTime(v.GetValidSince())),
		ValidUntil:   new(decodeLinkTime(v.GetValidUntil())),
		MaxVisits:    new(v.MaxVisits),
		Crawlable:    new(v.GetCrawlable()),
		ForwardQuery: new(v.GetForwardQuery()),
	}
}

func decodeLinkTime(v *timestamppb.Timestamp) *time.Time {
	if v == nil {
		return nil
	}
	return new(v.AsTime())
}

func decodeLinkCodeLength(v *int32) *int {
	if v == nil {
		return nil
	}
	return new(int(*v))
}
