package handlers

import (
	"net/http"
	"strconv"

	"github.com/getarcaneapp/arcane/backend/internal/models"
	"github.com/getarcaneapp/arcane/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type ComplianceHandler struct {
	service *services.DriftDetectionService
}

func NewComplianceHandler(service *services.DriftDetectionService) *ComplianceHandler {
	return &ComplianceHandler{service: service}
}

func (h *ComplianceHandler) RegisterRoutes(group *gin.RouterGroup) {
	r := group.Group("/environments/:id/compliance")
	r.POST("/baselines", h.createBaseline)
	r.GET("/baselines", h.listBaselines)
	r.GET("/baselines/:baselineId", h.getBaseline)
	r.POST("/baselines/:baselineId/activate", h.activateBaseline)
	r.DELETE("/baselines/:baselineId", h.deleteBaseline)
	r.POST("/detect", h.detect)
	r.GET("/drifts", h.listDrifts)
	r.POST("/drifts/:driftId/acknowledge", h.acknowledge)
	r.POST("/drifts/:driftId/ignore", h.ignore)
	r.GET("/history", h.history)
}

type complianceEnvelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Total   int64  `json:"total,omitempty"`
}
type baselineRequest struct {
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	Containers  map[string]models.ContainerConfig `json:"containers"`
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, complianceEnvelope{Success: true, Data: data})
}
func fail(c *gin.Context, status int, err error) {
	c.JSON(status, complianceEnvelope{Success: false, Error: err.Error()})
}
func params(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	return limit, offset
}

func (h *ComplianceHandler) createBaseline(c *gin.Context) {
	var req baselineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err)
		return
	}
	b, err := h.service.CaptureBaselineFromConfigs(c.Request.Context(), c.Param("id"), req.Name, req.Description, c.GetHeader("X-User-ID"), req.Containers)
	if err != nil {
		fail(c, 500, err)
		return
	}
	c.JSON(http.StatusCreated, complianceEnvelope{Success: true, Data: b})
}
func (h *ComplianceHandler) listBaselines(c *gin.Context) {
	l, o := params(c)
	v, t, e := h.service.ListBaselines(c.Request.Context(), c.Param("id"), l, o)
	if e != nil {
		fail(c, 500, e)
		return
	}
	c.JSON(200, complianceEnvelope{Success: true, Data: v, Total: t})
}
func (h *ComplianceHandler) getBaseline(c *gin.Context) {
	v, e := h.service.GetBaseline(c.Request.Context(), c.Param("baselineId"))
	if e != nil {
		fail(c, 500, e)
		return
	}
	if v == nil {
		c.JSON(404, complianceEnvelope{Success: false, Error: "baseline not found"})
		return
	}
	ok(c, v)
}
func (h *ComplianceHandler) activateBaseline(c *gin.Context) {
	if e := h.service.SetActiveBaseline(c.Request.Context(), c.Param("baselineId")); e != nil {
		fail(c, 404, e)
		return
	}
	ok(c, map[string]bool{"activated": true})
}
func (h *ComplianceHandler) deleteBaseline(c *gin.Context) {
	if e := h.service.DeleteBaseline(c.Request.Context(), c.Param("baselineId")); e != nil {
		fail(c, 500, e)
		return
	}
	ok(c, map[string]bool{"deleted": true})
}
func (h *ComplianceHandler) detect(c *gin.Context) {
	var req struct {
		Containers map[string]models.ContainerConfig `json:"containers"`
	}
	if e := c.ShouldBindJSON(&req); e != nil {
		fail(c, 400, e)
		return
	}
	v, e := h.service.DetectDriftFromConfigs(c.Request.Context(), c.Param("id"), req.Containers)
	if e != nil {
		status := 500
		if e.Error() == "no active baseline" {
			status = 400
		}
		fail(c, status, e)
		return
	}
	ok(c, v)
}
func (h *ComplianceHandler) listDrifts(c *gin.Context) {
	l, o := params(c)
	v, t, e := h.service.GetDriftRecords(c.Request.Context(), c.Param("id"), l, o)
	if e != nil {
		fail(c, 500, e)
		return
	}
	c.JSON(200, complianceEnvelope{Success: true, Data: v, Total: t})
}
func (h *ComplianceHandler) acknowledge(c *gin.Context) {
	if e := h.service.AcknowledgeDrift(c.Request.Context(), c.Param("driftId")); e != nil {
		fail(c, 500, e)
		return
	}
	ok(c, map[string]bool{"acknowledged": true})
}
func (h *ComplianceHandler) ignore(c *gin.Context) {
	if e := h.service.IgnoreDrift(c.Request.Context(), c.Param("driftId")); e != nil {
		fail(c, 500, e)
		return
	}
	ok(c, map[string]bool{"ignored": true})
}
func (h *ComplianceHandler) history(c *gin.Context) {
	l, o := params(c)
	v, e := h.service.GetComplianceHistory(c.Request.Context(), c.Param("id"), l, o)
	if e != nil {
		fail(c, 500, e)
		return
	}
	ok(c, v)
}
