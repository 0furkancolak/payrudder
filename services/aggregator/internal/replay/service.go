package replay

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Service implements aggregatorv1.ReplayServiceServer (operators only).
type Service struct {
	aggregatorv1.UnimplementedReplayServiceServer
	m *Manager
}

// NewService returns the gRPC service of a Manager.
func NewService(m *Manager) *Service { return &Service{m: m} }

func ids(tenantID, jobID string) (uuid.UUID, uuid.UUID, error) {
	t, err := errkey.ParseUUID("aggregator.replay.bad_tenant_id", "tenant_id", tenantID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if jobID == "" {
		return t, uuid.Nil, nil
	}
	j, err := errkey.ParseUUID("aggregator.replay.bad_job_id", "job_id", jobID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return t, j, nil
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalid):
		return errkey.New(codes.InvalidArgument, "aggregator.replay.invalid", err.Error())
	case errors.Is(err, ErrNotFound):
		return errkey.New(codes.NotFound, "aggregator.replay.not_found", "no such replay job")
	case errors.Is(err, ErrOpen), errors.Is(err, ErrState):
		return errkey.New(codes.FailedPrecondition, "aggregator.replay.bad_state", err.Error())
	}
	return status.Error(codes.Internal, "internal error")
}

func protoOf(j *Job) *aggregatorv1.ReplayJob {
	st := aggregatorv1.ReplayStatus_REPLAY_STATUS_UNSPECIFIED
	switch j.Status {
	case Running:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_RUNNING
	case CatchingUp:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_CATCHING_UP
	case Ready:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_READY
	case Promoted:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_PROMOTED
	case Discarded:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_DISCARDED
	case Failed:
		st = aggregatorv1.ReplayStatus_REPLAY_STATUS_FAILED
	}
	out := &aggregatorv1.ReplayJob{
		Id: j.ID.String(), TenantId: j.TenantID.String(), FromPeriodId: j.From, ToPeriodId: j.To, SourceGeneration: j.SourceGeneration,
		TargetGeneration: j.TargetGeneration, Status: st, Reason: j.Reason, StartOffsets: j.StartOffsets, EndOffsets: j.EndOffsets,
		DualWriteFrom: j.DualWriteFrom, LastError: j.LastError, CreateTime: timestamppb.New(j.CreatedAt),
	}
	if j.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*j.CompletedAt)
	}
	if v := j.Verification; v != nil {
		pv := &aggregatorv1.ReplayVerification{RowsCompared: v.RowsCompared, RowsChanged: v.RowsChanged, RowsOnlyInOld: v.RowsOnlyInOld, RowsOnlyInNew: v.RowsOnlyInNew}
		for _, d := range v.Diffs {
			pv.Diffs = append(pv.Diffs, &aggregatorv1.ReplayDiff{Kind: d.Kind, CustomerId: d.Customer, MeterCode: d.Meter, PeriodId: d.Period,
				OriginPeriodId: d.OriginPeriod, OldQuantity: d.OldQuantity, NewQuantity: d.NewQuantity, OldEventCount: d.OldEventCount, NewEventCount: d.NewEventCount})
		}
		out.Verification = pv
	}
	return out
}

// StartReplay implements aggregatorv1.ReplayServiceServer.
func (s *Service) StartReplay(ctx context.Context, req *aggregatorv1.StartReplayRequest) (*aggregatorv1.StartReplayResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, _, err := ids(req.GetTenantId(), "")
	if err != nil {
		return nil, err
	}
	j, err := s.m.Start(ctx, t, req.GetFromPeriodId(), req.GetToPeriodId(), req.GetReason())
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.StartReplayResponse{Job: protoOf(j)}, nil
}

// GetReplayJob implements aggregatorv1.ReplayServiceServer.
func (s *Service) GetReplayJob(ctx context.Context, req *aggregatorv1.GetReplayJobRequest) (*aggregatorv1.GetReplayJobResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, j, err := ids(req.GetTenantId(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	job, err := s.m.Get(ctx, t, j)
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.GetReplayJobResponse{Job: protoOf(job)}, nil
}

// PromoteGeneration implements aggregatorv1.ReplayServiceServer.
func (s *Service) PromoteGeneration(ctx context.Context, req *aggregatorv1.PromoteGenerationRequest) (*aggregatorv1.PromoteGenerationResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, j, err := ids(req.GetTenantId(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	job, err := s.m.Promote(ctx, t, j)
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.PromoteGenerationResponse{Job: protoOf(job)}, nil
}

// DiscardGeneration implements aggregatorv1.ReplayServiceServer.
func (s *Service) DiscardGeneration(ctx context.Context, req *aggregatorv1.DiscardGenerationRequest) (*aggregatorv1.DiscardGenerationResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	t, j, err := ids(req.GetTenantId(), req.GetJobId())
	if err != nil {
		return nil, err
	}
	job, err := s.m.Discard(ctx, t, j)
	if err != nil {
		return nil, mapErr(err)
	}
	return &aggregatorv1.DiscardGenerationResponse{Job: protoOf(job)}, nil
}
