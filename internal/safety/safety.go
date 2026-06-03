package safety

import coresafety "github.com/JiangHe12/opskit-core/safety"

type Risk = coresafety.Risk

const (
	R0 = coresafety.R0
	R1 = coresafety.R1
	R2 = coresafety.R2
	R3 = coresafety.R3
)

type (
	ContextMeta = coresafety.ContextMeta
	Options     = coresafety.Options
	AllowFlag   = coresafety.AllowFlag
)

const (
	RoleReader = coresafety.RoleReader
	RoleWriter = coresafety.RoleWriter
	RoleAdmin  = coresafety.RoleAdmin
)

const (
	AllowDestructive     AllowFlag = "allow-destructive"
	AllowNoWhere         AllowFlag = "allow-no-where"
	AllowProductionPrune AllowFlag = "allow-production-prune"
)

var (
	EffectiveRisk        = coresafety.EffectiveRisk
	Authorize            = coresafety.Authorize
	ValidateBackupPolicy = coresafety.ValidateBackupPolicy
)
