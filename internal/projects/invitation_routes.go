package projects

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"sitetrack/internal/authentication"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/infra/ratelimit"
	"sitetrack/internal/infra/request"
	"time"
)

type invitationPath struct {
	ID           string `json:"id" uri:"id" binding:"required,max=32"`
	InvitationID string `json:"invitation_id" uri:"invitation_id" binding:"required,uuid"`
}
type ownInvitationPath struct {
	InvitationID string `json:"invitation_id" uri:"invitation_id" binding:"required,uuid"`
}

func registerInvitations(router *gin.RouterGroup, service *Service, accounts authentication.Accounts, contracts *openapi.Registry, enabled bool, logger *slog.Logger) error {
	invite, err := request.NewBody[InviteInput]()
	if err != nil {
		return err
	}
	respond, err := request.NewBody[RespondInput]()
	if err != nil {
		return err
	}
	listing, err := request.NewContract[InvitationListInput]()
	if err != nil {
		return err
	}
	projectPath, err := request.NewContract[projectPath]()
	if err != nil {
		return err
	}
	invitationPath, err := request.NewContract[invitationPath]()
	if err != nil {
		return err
	}
	ownPath, err := request.NewContract[ownInvitationPath]()
	if err != nil {
		return err
	}
	group := router.Group("", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	for _, endpoint := range []struct {
		method, path string
		operation    openapi.Operation
		policy       ratelimit.Policy
		handler      gin.HandlerFunc
	}{
		{"POST", "/projects/:id/invitations", inviteContract(invite.Schema, projectPath.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectPath.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			input, err := invite.Decode(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Invite(c.Request.Context(), principal.User.ID, path.ID, uuid.MustParse(input.InviteeID))
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusCreated, result)
		}},
		{"GET", "/projects/:id/invitations", invitationsContract(listing.Schema, projectPath.Schema), ratelimit.Policy{RequestsPerSecond: 10, Burst: 20, RequestsPerWindow: 300, Window: time.Minute}, func(c *gin.Context) {
			path, err := projectPath.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			input, err := listing.DecodeQuery(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Invitations(c.Request.Context(), principal.User.ID, path.ID, input)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"DELETE", "/projects/:id/invitations/:invitation_id", withdrawContract(invitationPath.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := invitationPath.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Withdraw(c.Request.Context(), principal.User.ID, path.ID, uuid.MustParse(path.InvitationID))
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
		{"POST", "/invitations/:invitation_id/respond", respondContract(respond.Schema, ownPath.Schema), ratelimit.Policy{RequestsPerSecond: 2, Burst: 5, RequestsPerWindow: 60, Window: time.Minute}, func(c *gin.Context) {
			path, err := ownPath.DecodePath(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			input, err := respond.Decode(c)
			if err != nil {
				projectError(c, logger, ErrInvalid)
				return
			}
			principal, _ := authentication.CurrentPrincipal(c.Request.Context())
			result, err := service.Respond(c.Request.Context(), principal.User.ID, uuid.MustParse(path.InvitationID), input.Action)
			if err != nil {
				projectError(c, logger, err)
				return
			}
			c.JSON(http.StatusOK, result)
		}},
	} {
		limiter, err := ratelimit.New(endpoint.policy)
		if err != nil {
			return err
		}
		if err = contracts.Register(group, endpoint.method, endpoint.path, endpoint.operation, limiter.Middleware(enabled), authentication.RequireSession(accounts, logger), endpoint.handler); err != nil {
			return err
		}
	}
	return nil
}
