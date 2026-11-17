package action

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"

	"github.com/example/cos-workflow/internal/components"
	"github.com/example/cos-workflow/internal/model"
)

type Request struct {
	Workflow  *model.Workflow
	Run       *model.Run
	Context   *model.RunContext
	Component *model.RunComponent
}

type Result struct {
	Output     map[string]any
	Path       string
	Stop       bool
	StopReason string
}

type Executor struct {
	Files    *FileClient
	Notifier *Notifier
}

func NewExecutor(files *FileClient, notifier *Notifier) *Executor {
	return &Executor{Files: files, Notifier: notifier}
}

func (e *Executor) Execute(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	if req == nil || req.Component == nil || req.Context == nil {
		return Result{}, fmt.Errorf("invalid action request")
	}
	switch req.Component.TypeKey {
	case components.TypeProcessRename:
		return e.rename(ctx, req, dryRun)
	case components.TypeProcessRemove:
		return e.remove(ctx, req, dryRun)
	case components.TypeProcessCondition:
		return e.condition(ctx, req, dryRun)
	case components.TypeOutputCopy:
		return e.copy(ctx, req, dryRun)
	case components.TypeOutputWebhook:
		return e.webhook(ctx, req, dryRun)
	case components.TypeOutputEmail:
		return e.email(ctx, req, dryRun)
	default:
		return Result{}, fmt.Errorf("unsupported component type %s", req.Component.TypeKey)
	}
}

func (e *Executor) rename(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeRename(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	rc := req.Context
	var target string
	switch cfg.Mode {
	case "regex":
		re, err := regexp.Compile(cfg.Pattern)
		if err != nil {
			return Result{}, err
		}
		base := re.ReplaceAllString(path.Base(rc.Path), cfg.Replacement)
		target, err = cleanRelPath(path.Join(path.Dir(rc.Path), base))
		if err != nil {
			return Result{}, err
		}
	default:
		target, err = renderPath(cfg.Template, rc)
		if err != nil {
			return Result{}, err
		}
	}
	if target == rc.Path {
		return Result{Output: map[string]any{"source_path": rc.Path, "destination_path": target}}, nil
	}
	if dryRun {
		return Result{
			Output: map[string]any{"source_path": rc.Path, "destination_path": target, "collision": cfg.Collision, "dry_run": true},
			Path:   target,
		}, nil
	}
	finalPath, err := e.renameWithCollision(ctx, req, target, cfg.Collision)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Output: map[string]any{
			"source_path": rc.Path, "destination_path": finalPath,
			"bytes": rc.Size, "collision": cfg.Collision,
		},
		Path: finalPath,
	}, nil
}

func (e *Executor) renameWithCollision(ctx context.Context, req *Request, target, collision string) (string, error) {
	rc := req.Context
	overwrite := collision == components.CollisionOverwrite
	err := e.Files.Rename(ctx, rc.Username, rc.Path, target, overwrite, corr(req))
	if err == nil {
		return target, nil
	}
	if IsNotFound(err) {
		if stat, serr := e.Files.Stat(ctx, rc.Username, target, corr(req)); serr == nil && stat.Size == rc.Size {
			return target, nil
		}
	}
	if IsConflict(err) && collision == components.CollisionSuffix {
		suffixed := addSuffix(target, rc.RunID.String())
		if err2 := e.Files.Rename(ctx, rc.Username, rc.Path, suffixed, false, corr(req)); err2 == nil {
			return suffixed, nil
		} else {
			return "", err2
		}
	}
	return "", err
}

func (e *Executor) condition(_ context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeCondition(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	rc := req.Context
	target := rc.Path
	if cfg.PathTarget == components.PathTargetBase {
		target = path.Base(rc.Path)
	}
	matched := true
	if cfg.PathRegex != "" {
		re, err := regexp.Compile(cfg.PathRegex)
		if err != nil {
			return Result{}, err
		}
		matched = re.MatchString(target)
	}
	if matched && len(cfg.MIMEPatterns) > 0 {
		matched = components.MatchMIMEPatterns(rc.MIME, cfg.MIMEPatterns)
	}
	if matched && cfg.MIMERegex != "" {
		re, err := regexp.Compile(cfg.MIMERegex)
		if err != nil {
			return Result{}, err
		}
		matched = re.MatchString(components.NormalizeMediaType(rc.MIME))
	}
	if cfg.Negate {
		matched = !matched
	}
	output := map[string]any{
		"condition": matched,
		"negate":    cfg.Negate,
		"path":      rc.Path,
		"mime":      rc.MIME,
		"dry_run":   dryRun,
	}
	if !matched {
		return Result{
			Output:     output,
			Stop:       true,
			StopReason: "condition evaluated false",
		}, nil
	}
	return Result{Output: output}, nil
}

func (e *Executor) remove(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeRemove(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	rc := req.Context
	target := rc.Path
	if cfg.PathTemplate != "" {
		target, err = renderPath(cfg.PathTemplate, rc)
		if err != nil {
			return Result{}, err
		}
	}
	if dryRun {
		return Result{Output: map[string]any{"path": target, "missing_ok": cfg.MissingOK, "dry_run": true}}, nil
	}
	if err := e.Files.Remove(ctx, rc.Username, target, cfg.MissingOK, corr(req)); err != nil {
		if !(cfg.MissingOK && IsNotFound(err)) {
			return Result{}, err
		}
	}
	return Result{Output: map[string]any{"path": target, "missing_ok": cfg.MissingOK}}, nil
}

func (e *Executor) copy(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeCopy(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	rc := req.Context
	target, err := renderPath(cfg.DestinationPath, rc)
	if err != nil {
		return Result{}, err
	}
	dstUser := cfg.DestinationUsername
	if dstUser == "" {
		dstUser = rc.Username
	}
	if dryRun {
		return Result{Output: map[string]any{
			"source_path": rc.Path, "destination_username": dstUser,
			"destination_path": target, "collision": cfg.Collision, "dry_run": true,
		}}, nil
	}
	finalPath, err := e.copyWithCollision(ctx, req, dstUser, target, cfg.Collision)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"source_path": rc.Path, "destination_username": dstUser,
		"destination_path": finalPath, "bytes": rc.Size, "collision": cfg.Collision,
	}}, nil
}

func (e *Executor) copyWithCollision(ctx context.Context, req *Request, dstUser, target, collision string) (string, error) {
	rc := req.Context
	overwrite := collision == components.CollisionOverwrite
	err := e.Files.Copy(ctx, rc.Username, rc.Path, dstUser, target, overwrite, corr(req))
	if err == nil {
		return target, nil
	}
	if IsConflict(err) && collision == components.CollisionSuffix {
		suffixed := addSuffix(target, rc.RunID.String())
		if err2 := e.Files.Copy(ctx, rc.Username, rc.Path, dstUser, suffixed, false, corr(req)); err2 == nil {
			return suffixed, nil
		} else {
			return "", err2
		}
	}
	return "", err
}

func (e *Executor) webhook(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeWebhook(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	if dryRun {
		if e.Notifier == nil {
			return Result{Output: map[string]any{"channel": "webhook", "url": cfg.URL, "dry_run": true}}, nil
		}
		out, err := e.Notifier.PreviewWebhook(ctx, req, cfg)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: out}, nil
	}
	if e.Notifier == nil {
		return Result{}, fmt.Errorf("notifier is not configured")
	}
	out, err := e.Notifier.SendWebhook(ctx, req, cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: out}, nil
}

func (e *Executor) email(ctx context.Context, req *Request, dryRun bool) (Result, error) {
	cfg, err := components.DecodeEmail(req.Component.Config)
	if err != nil {
		return Result{}, err
	}
	if dryRun {
		if e.Notifier == nil {
			return Result{Output: map[string]any{"channel": "email", "to": cfg.To, "dry_run": true}}, nil
		}
		out, err := e.Notifier.PreviewEmail(req, cfg)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: out}, nil
	}
	if e.Notifier == nil {
		return Result{}, fmt.Errorf("notifier is not configured")
	}
	out, err := e.Notifier.SendEmail(ctx, req, cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: out}, nil
}

func corr(req *Request) Correlation {
	c := Correlation{}
	if req.Workflow != nil {
		c.WorkflowID = req.Workflow.ID.String()
	}
	if req.Run != nil {
		c.RunID = req.Run.ID.String()
	}
	if req.Component != nil {
		c.ComponentID = req.Component.ID.String()
	}
	return c
}

// helper used by tests and notification payloads
func eventValue(run *model.Run) any {
	if run == nil || len(run.Event) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(run.Event, &v); err != nil {
		return nil
	}
	return v
}
