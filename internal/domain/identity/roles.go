package identity

// HasRole 判断令牌声明是否包含任一给定角色。
func (claims AccessTokenClaims) HasRole(roles ...string) bool {
	for _, wanted := range roles {
		for _, owned := range claims.Roles {
			if owned == wanted {
				return true
			}
		}
	}
	return false
}

// 平台内的角色代码常量，对应 roles 表中的 code。
const (
	RolePlayer   = "player"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)
