package catalog

import (
	"fmt"
	"strings"

	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"
	"github.com/pingcap/tidb/pkg/parser/mysql"

	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

// Build transforme une suite d'instructions SQL, dans l'ordre du fichier, en
// catalogue normalisé. Les instructions non reconnues sont signalées par un
// avertissement plutôt que silencieusement ignorées.
func Build(stmts []ast.StmtNode, d dialect.Dialect, source string) (*Catalog, []string) {
	b := &builder{
		dialect: d,
		tables:  map[string]*Table{},
	}
	for _, stmt := range stmts {
		b.stmt(stmt)
	}

	cat := &Catalog{Source: source, Dialect: d.ID}
	seen := map[string]bool{}
	for _, name := range b.order {
		if seen[name] {
			continue
		}
		seen[name] = true
		if t, ok := b.tables[name]; ok {
			cat.Tables = append(cat.Tables, t)
		}
	}
	cat.Views = append(cat.Views, b.views...)
	for _, t := range cat.Tables {
		finalizeTable(t, d, &b.warnings)
	}
	cat.Warnings = b.warnings
	cat.Sort()
	return cat, b.warnings
}

type builder struct {
	dialect  dialect.Dialect
	tables   map[string]*Table
	order    []string
	views    []*View
	warnings []string
}

func (b *builder) warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, w := range b.warnings {
		if w == msg {
			return
		}
	}
	b.warnings = append(b.warnings, msg)
}

func (b *builder) stmt(stmt ast.StmtNode) {
	switch s := stmt.(type) {
	case *ast.CreateTableStmt:
		b.createTable(s)
	case *ast.AlterTableStmt:
		b.alterTable(s)
	case *ast.DropTableStmt:
		for _, t := range s.Tables {
			if s.IsView {
				b.dropView(t.Name.O)
			} else {
				b.dropTable(t.Name.O)
			}
		}
	case *ast.CreateViewStmt:
		b.createView(s)
	case *ast.CreateDatabaseStmt, *ast.UseStmt, *ast.SetStmt,
		*ast.SetPwdStmt, *ast.SetSessionStatesStmt, *ast.FlushStmt, *ast.LockTablesStmt,
		*ast.UnlockTablesStmt, *ast.BeginStmt, *ast.CommitStmt, *ast.RollbackStmt:
		// Sans effet sur la structure décrite.
	default:
		b.warnf("instruction ignorée (%T)", stmt)
	}
}

func (b *builder) createTable(s *ast.CreateTableStmt) {
	name := s.Table.Name.O
	if s.TemporaryKeyword != ast.TemporaryNone {
		// Une table temporaire n'est pas dans information_schema : elle ne fait
		// pas partie du schéma physique.
		b.warnf("table temporaire ignorée : %s", name)
		return
	}
	if s.Partition != nil {
		b.warnf("cloisonnement (PARTITION BY) non représenté pour %s", name)
	}
	t := &Table{Name: name}
	if s.ReferTable != nil {
		// CREATE TABLE ... LIKE : le contenu de la table source n'est pas décrit ici.
		b.warnf("CREATE TABLE %s LIKE %s : colonnes non reproduites", name, s.ReferTable.Name.O)
	} else {
		facts := make([]columnFacts, 0, len(s.Cols))
		for _, cd := range s.Cols {
			col, f := columnFromDef(cd)
			t.Columns = append(t.Columns, col)
			facts = append(facts, f)
		}
		applyTimestampRule(t.Columns, facts, b.dialect)
		t.Indexes, t.ForeignKeys, t.Checks = fromConstraints(s.Constraints, &b.warnings, b.dialect)
	}
	t.Engine, t.Charset, t.Collation, t.Comment = tableOptions(s.Options)

	if !contains(b.order, name) {
		b.order = append(b.order, name)
	}
	b.tables[name] = t
}

func (b *builder) dropTable(name string) {
	delete(b.tables, name)
	if !contains(b.order, name) {
		b.order = append(b.order, name)
	}
}

func (b *builder) dropView(name string) {
	for i, v := range b.views {
		if v.Name == name {
			b.views = append(b.views[:i], b.views[i+1:]...)
			return
		}
	}
}

func (b *builder) createView(s *ast.CreateViewStmt) {
	name := s.ViewName.Name.O
	view := &View{Name: name}
	for _, c := range s.SchemaCols {
		view.Columns = append(view.Columns, c.O)
	}
	if len(view.Columns) == 0 {
		for _, c := range s.Cols {
			view.Columns = append(view.Columns, c.O)
		}
	}
	if s.Select != nil {
		view.Definition = strings.TrimSpace(s.Select.Text())
	}
	if len(view.Columns) == 0 {
		view.Columns = selectColumns(s.Select)
	}
	b.dropView(name)
	b.views = append(b.views, view)
}

// selectColumns tente de nommer les colonnes d'une vue qui n'en déclare pas la
// liste : le serveur, lui, les expose. Un `SELECT *` reste indéterminé, faute de
// connaître les tables sources.
func selectColumns(sel ast.StmtNode) []string {
	stmt, ok := sel.(*ast.SelectStmt)
	if !ok || stmt.Fields == nil {
		return nil
	}
	var out []string
	for _, field := range stmt.Fields.Fields {
		if field.WildCard != nil {
			return nil
		}
		if field.AsName.O != "" {
			out = append(out, field.AsName.O)
			continue
		}
		if text := strings.TrimSpace(field.Expr.Text()); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func (b *builder) alterTable(s *ast.AlterTableStmt) {
	name := s.Table.Name.O
	for _, spec := range s.Specs {
		if spec == nil {
			continue
		}
		switch spec.Tp {
		case ast.AlterTableRenameTable:
			if spec.NewTable != nil {
				if t, ok := b.tables[name]; ok {
					delete(b.tables, name)
					extra := spec.NewTable.Name.O
					t.Name = extra
					b.tables[extra] = t
					// L'ordre de collecte porte les noms : sans cette mise à jour, la
					// table renommée disparaît des livrables sans un mot.
					for i, listed := range b.order {
						if listed == name {
							b.order[i] = extra
						}
					}
					name = extra
				}
			}
			continue
		case ast.AlterTableLock, ast.AlterTableAlgorithm, ast.AlterTableForce,
			ast.AlterTableEnableKeys, ast.AlterTableDisableKeys, ast.AlterTableOrderByColumns,
			ast.AlterTableWithValidation, ast.AlterTableWithoutValidation,
			ast.AlterTableIndexInvisible, ast.AlterTableOption:
			continue
		}

		t, ok := b.tables[name]
		if !ok {
			b.warnf("ALTER TABLE %s : table inconnue dans ce dump (alter ignoré)", name)
			continue
		}
		b.applySpec(t, spec)
	}
}

func (b *builder) applySpec(t *Table, spec *ast.AlterTableSpec) {
	switch spec.Tp {
	case ast.AlterTableAddColumns:
		for _, cd := range spec.NewColumns {
			col, _ := columnFromDef(cd)
			insertColumn(t, col, spec.Position)
		}
	case ast.AlterTableModifyColumn, ast.AlterTableChangeColumn:
		for _, cd := range spec.NewColumns {
			col, _ := columnFromDef(cd)
			old := col.Name
			if spec.Tp == ast.AlterTableChangeColumn && spec.OldColumnName != nil {
				old = spec.OldColumnName.Name.O
			}
			if idx := columnIndex(t, old); idx >= 0 {
				col.Ordinal = t.Columns[idx].Ordinal
				t.Columns[idx] = col
				renameColumnRefs(t, old, col.Name)
				positionColumn(t, col.Name, spec.Position)
			} else {
				insertColumn(t, col, spec.Position)
			}
		}
	case ast.AlterTableRenameColumn:
		if spec.OldColumnName != nil && spec.NewColumnName != nil {
			old, newName := spec.OldColumnName.Name.O, spec.NewColumnName.Name.O
			if idx := columnIndex(t, old); idx >= 0 {
				t.Columns[idx].Name = newName
			}
			renameColumnRefs(t, old, newName)
		}
	case ast.AlterTableDropColumn:
		if spec.OldColumnName != nil {
			dropColumn(t, spec.OldColumnName.Name.O)
		}
	case ast.AlterTableAddConstraint:
		cons := spec.NewConstraints
		if spec.Constraint != nil {
			cons = append(cons, spec.Constraint)
		}
		ixs, fks, checks := fromConstraints(cons, &b.warnings, b.dialect)
		t.Indexes = append(t.Indexes, ixs...)
		t.ForeignKeys = append(t.ForeignKeys, fks...)
		t.Checks = append(t.Checks, checks...)
	case ast.AlterTableDropPrimaryKey:
		t.Indexes = removeIndex(t.Indexes, func(ix *Index) bool { return ix.Kind == KindPrimary })
	case ast.AlterTableDropIndex:
		t.Indexes = removeIndex(t.Indexes, func(ix *Index) bool { return strings.EqualFold(ix.Name, spec.Name) })
	case ast.AlterTableRenameIndex:
		for _, ix := range t.Indexes {
			if strings.EqualFold(ix.Name, spec.FromKey.O) {
				ix.Name = spec.ToKey.O
			}
		}
	case ast.AlterTableDropForeignKey:
		t.ForeignKeys = removeFK(t.ForeignKeys, func(fk *ForeignKey) bool { return strings.EqualFold(fk.Name, spec.Name) })
	case ast.AlterTableDropCheck:
		t.Checks = removeCheck(t.Checks, func(c *Check) bool { return strings.EqualFold(c.Name, spec.Name) })
	case ast.AlterTableAlterColumn:
		for _, cd := range spec.NewColumns {
			if cd == nil || cd.Name == nil {
				continue
			}
			col := t.Column(cd.Name.Name.O)
			if col == nil {
				continue
			}
			// « DROP DEFAULT » est analysé comme une colonne sans option.
			if len(cd.Options) == 0 {
				col.Default = nil
				continue
			}
			for _, opt := range cd.Options {
				switch opt.Tp {
				case ast.ColumnOptionNotNull:
					col.Nullable = false
				case ast.ColumnOptionNull:
					col.Nullable = true
				case ast.ColumnOptionNoOption:
					if isNullLiteral(opt.Expr) {
						col.Default = nil
					} else {
						value := formatDefault(opt.Expr, col.Type)
						col.Default = &value
					}
				}
			}
		}
	default:
		b.warnf("clause ALTER TABLE ignorée (type %d)", int(spec.Tp))
	}
}

// literalString rend la chaîne portée par une expression littérale. Retourner la
// valeur de StrValue ne marche pas pour tous les cas : le commentaire de colonne
// y est vide, alors que l'expression porte bien le texte.
func literalString(e ast.ExprNode) string {
	if e == nil {
		return ""
	}
	if v, ok := e.(interface{ GetDatumString() string }); ok {
		return v.GetDatumString()
	}
	return strings.Trim(e.Text(), "'")
}

// columnFacts retient les attributs déclarés explicitement, nécessaires aux
// règles qui ne peuvent être appliquées qu'après coup (premier TIMESTAMP).
type columnFacts struct {
	notNull    bool
	nullAttr   bool
	hasDefault bool
	hasUpdate  bool
}

// columnFromDef construit une colonne et les attributs explicites qui l'accompagnent.
func columnFromDef(cd *ast.ColumnDef) (*Column, columnFacts) {
	col := &Column{Name: cd.Name.Name.O}
	if cd.Tp != nil {
		col.Type = cd.Tp.InfoSchemaStr()
		if mysql.HasZerofillFlag(cd.Tp.GetFlag()) && !strings.Contains(strings.ToLower(col.Type), "zerofill") {
			col.Type += " zerofill"
		}
		col.Charset = cd.Tp.GetCharset()
		col.Collation = cd.Tp.GetCollate()
	}

	facts := columnFacts{}
	var (
		onUpdate      string
		generatedExpr string
		stored        bool
	)

	for _, opt := range cd.Options {
		if opt == nil {
			continue
		}
		switch opt.Tp {
		case ast.ColumnOptionNotNull:
			facts.notNull = true
		case ast.ColumnOptionNull:
			facts.nullAttr = true
		case ast.ColumnOptionPrimaryKey:
			facts.notNull = true
			col.Key = "PRI"
		case ast.ColumnOptionUniqKey:
			col.Key = "UNI"
		case ast.ColumnOptionAutoIncrement:
			col.Extra = "auto_increment"
		case ast.ColumnOptionDefaultValue:
			facts.hasDefault = true
			if !isNullLiteral(opt.Expr) {
				value := formatDefault(opt.Expr, col.Type)
				col.Default = &value
			}
		case ast.ColumnOptionOnUpdate:
			facts.hasUpdate = true
			onUpdate = formatDefault(opt.Expr, col.Type)
		case ast.ColumnOptionComment:
			// Le parseur TiDB ne renseigne pas StrValue pour un commentaire de
			// colonne : la valeur est dans l'expression.
			col.Comment = literalString(opt.Expr)
		case ast.ColumnOptionCollate:
			col.Collation = opt.StrValue
		case ast.ColumnOptionGenerated:
			generatedExpr = strings.TrimSpace(opt.Expr.Text())
			stored = opt.Stored
		}
	}

	col.Nullable = !facts.notNull
	if facts.nullAttr {
		col.Nullable = true
	}
	if onUpdate != "" {
		col.Extra = joinExtra(col.Extra, "on update "+onUpdate)
	}
	if generatedExpr != "" {
		col.Generated = generatedExpr
		kind := "VIRTUAL GENERATED"
		if stored {
			kind = "STORED GENERATED"
		}
		col.Extra = joinExtra(col.Extra, kind)
	}
	return col, facts
}

func joinExtra(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// applyTimestampRule reproduit explicit_defaults_for_timestamp=OFF.
//
// La règle est **positionnelle** : la première colonne TIMESTAMP de la table
// occupe la place, qu'elle soit déclarée NULL ou non. Cette colonne, si elle
// n'est pas déclarée NULL :
//   - sans DEFAULT explicite : reçoit DEFAULT CURRENT_TIMESTAMP et
//     ON UPDATE CURRENT_TIMESTAMP ;
//   - avec un DEFAULT explicite : ne reçoit rien de plus (pas d'ON UPDATE
//     implicite) ;
//
// Les TIMESTAMP suivants, non déclarés NULL et sans DEFAULT, reçoivent le
// TIMESTAMP zéro. Toutes les colonnes TIMESTAMP non déclarées NULL deviennent
// NOT NULL.
//
// Comportement vérifié par l'oracle sur edge_timestamps et edge_ts_*.
func applyTimestampRule(cols []*Column, facts []columnFacts, d dialect.Dialect) {
	if !d.ImplicitFirstTimestamp {
		return
	}
	slotTaken := false
	for i, col := range cols {
		if i >= len(facts) || !isTimestampType(col.Type) {
			continue
		}
		f := facts[i]
		first := !slotTaken
		slotTaken = true

		if f.nullAttr {
			// Déclarée NULL : elle consomme la place de premier TIMESTAMP, mais
			// ne reçoit ni NOT NULL ni défaut implicite.
			continue
		}
		col.Nullable = false
		switch {
		case first && !f.hasDefault:
			value := "current_timestamp()"
			col.Default = &value
			if !f.hasUpdate {
				col.Extra = joinExtra(col.Extra, "on update current_timestamp()")
			}
		case first:
			// DEFAULT explicite : la règle ne s'applique pas.
		case !f.hasDefault:
			// Le serveur rapporte le TIMESTAMP zéro entre guillemets.
			value := "'0000-00-00 00:00:00'"
			col.Default = &value
		}
	}
}

func isTimestampType(columnType string) bool {
	base := columnType
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	return strings.EqualFold(strings.TrimSpace(base), "timestamp")
}

// fromConstraints traduit les contraintes de table en index, clés étrangères
// et contraintes CHECK.
func fromConstraints(cons []*ast.Constraint, warnings *[]string, d dialect.Dialect) ([]*Index, []*ForeignKey, []*Check) {
	var (
		indexes []*Index
		fks     []*ForeignKey
		checks  []*Check
	)
	for _, con := range cons {
		if con == nil {
			continue
		}
		switch con.Tp {
		case ast.ConstraintPrimaryKey:
			indexes = append(indexes, &Index{Name: "PRIMARY", Kind: KindPrimary, Columns: indexColumns(con.Keys)})
		case ast.ConstraintUniq, ast.ConstraintUniqKey, ast.ConstraintUniqIndex:
			indexes = append(indexes, &Index{
				Name:    defaultIndexName(con),
				Kind:    KindUnique,
				Columns: indexColumns(con.Keys),
				Comment: indexComment(con),
			})
		case ast.ConstraintKey, ast.ConstraintIndex:
			indexes = append(indexes, &Index{
				Name:    defaultIndexName(con),
				Kind:    indexKindFromOption(con),
				Columns: indexColumns(con.Keys),
				Comment: indexComment(con),
			})
		case ast.ConstraintFulltext:
			indexes = append(indexes, &Index{
				Name:    defaultIndexName(con),
				Kind:    KindFulltext,
				Columns: indexColumns(con.Keys),
				Comment: indexComment(con),
			})
		case ast.ConstraintForeignKey:
			fks = append(fks, foreignKeyFromConstraint(con))
		case ast.ConstraintCheck:
			check := &Check{Name: con.Name, Expr: exprSQL(con.Expr)}
			checks = append(checks, check)
			if !d.CheckEnforced {
				*warnings = append(*warnings, fmt.Sprintf(
					"contrainte CHECK %q déclarée mais ignorée par ce serveur", con.Name))
			}
		}
	}
	return indexes, fks, checks
}

// exprSQL rend une expression en SQL, comme le ferait SHOW CREATE TABLE.
// Le parseur ne renseigne pas Text() pour les nœuds qu'il fabrique — une
// contrainte CHECK ressortait donc avec une expression vide — mais Restore sait
// les écrire.
func exprSQL(e ast.ExprNode) string {
	if e == nil {
		return ""
	}
	var sb strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
	if err := e.Restore(ctx); err == nil && sb.Len() > 0 {
		return sb.String()
	}
	return strings.TrimSpace(e.Text())
}

func indexKindFromOption(con *ast.Constraint) Kind {
	if con.Option == nil {
		return KindKey
	}
	switch con.Option.Tp {
	case ast.IndexTypeRtree:
		return KindSpatial
	case ast.IndexTypeFulltext:
		return KindFulltext
	}
	return KindKey
}

func indexComment(con *ast.Constraint) string {
	if con.Option == nil {
		return ""
	}
	return con.Option.Comment
}

func defaultIndexName(con *ast.Constraint) string {
	if con.Name != "" {
		return con.Name
	}
	// MySQL nomme un index UNIQUE/KEY non nommé d'après sa première colonne.
	if len(con.Keys) > 0 && con.Keys[0].Column != nil {
		return con.Keys[0].Column.Name.O
	}
	return ""
}

func indexColumns(keys []*ast.IndexPartSpecification) []IndexColumn {
	out := make([]IndexColumn, 0, len(keys))
	for _, k := range keys {
		if k == nil || k.Column == nil {
			continue
		}
		// TiDB utilise types.UnspecifiedLength (-1) quand aucun préfixe n'est donné.
		prefix := 0
		if k.Length > 0 {
			prefix = k.Length
		}
		out = append(out, IndexColumn{Name: k.Column.Name.O, Prefix: prefix, Desc: k.Desc})
	}
	return out
}

func foreignKeyFromConstraint(con *ast.Constraint) *ForeignKey {
	fk := &ForeignKey{Name: con.Name}
	for _, k := range con.Keys {
		if k != nil && k.Column != nil {
			fk.Columns = append(fk.Columns, k.Column.Name.O)
		}
	}
	if con.Refer != nil {
		if con.Refer.Table != nil {
			fk.RefTable = con.Refer.Table.Name.O
		}
		for _, k := range con.Refer.IndexPartSpecifications {
			if k != nil && k.Column != nil {
				fk.RefColumns = append(fk.RefColumns, k.Column.Name.O)
			}
		}
		if con.Refer.OnDelete != nil {
			fk.OnDelete = con.Refer.OnDelete.ReferOpt.String()
		}
		if con.Refer.OnUpdate != nil {
			fk.OnUpdate = con.Refer.OnUpdate.ReferOpt.String()
		}
	}
	return fk
}

func tableOptions(opts []*ast.TableOption) (engine, charset, collation, comment string) {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		switch opt.Tp {
		case ast.TableOptionEngine:
			engine = opt.StrValue
		case ast.TableOptionCharset:
			charset = opt.StrValue
		case ast.TableOptionCollate:
			collation = opt.StrValue
		case ast.TableOptionComment:
			comment = opt.StrValue
		}
	}
	return engine, charset, collation, comment
}

// formatDefault rend une valeur par défaut comme le ferait
// information_schema.COLUMNS.COLUMN_DEFAULT : les chaînes gardent leurs
// guillemets, les nombres n'en ont pas.
func formatDefault(e ast.ExprNode, columnType string) string {
	if e == nil {
		return ""
	}
	if fn, ok := e.(*ast.FuncCallExpr); ok {
		name := strings.ToLower(fn.FnName.L)
		if len(fn.Args) == 0 {
			return name + "()"
		}
		// CURRENT_TIMESTAMP(3) : la précision fait partie du défaut.
		args := make([]string, 0, len(fn.Args))
		for _, a := range fn.Args {
			arg := strings.TrimSpace(a.Text())
			if arg == "" {
				// La précision d'un CURRENT_TIMESTAMP(3) vit dans la valeur de
				// l'expression, dont ni Text() ni GetDatumString() ne rendent
				// compte ici.
				if v, ok := a.(interface{ GetValue() any }); ok {
					if value := v.GetValue(); value != nil {
						arg = fmt.Sprint(value)
					}
				}
			}
			if arg == "" {
				arg = literalString(a)
			}
			args = append(args, arg)
		}
		return name + "(" + strings.Join(args, ",") + ")"
	}
	if v, ok := e.(interface{ GetDatumString() string }); ok {
		raw := v.GetDatumString()
		if !isNumericType(columnType) {
			if gv, ok := e.(interface{ GetValue() any }); ok {
				if _, isStr := gv.GetValue().(string); isStr {
					return "'" + strings.ReplaceAll(raw, "'", "''") + "'"
				}
			}
		}
		return raw
	}
	return strings.Trim(e.Text(), "'")
}

func isNullLiteral(e ast.ExprNode) bool {
	if e == nil {
		return true
	}
	if v, ok := e.(interface{ GetValue() any }); ok {
		return v.GetValue() == nil
	}
	return strings.EqualFold(strings.TrimSpace(e.Text()), "NULL")
}

var numericTypes = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true, "int": true, "integer": true,
	"bigint": true, "decimal": true, "numeric": true, "float": true, "double": true,
	"real": true, "bit": true, "year": true,
}

func isNumericType(columnType string) bool {
	return numericTypes[baseType(columnType)]
}

func baseType(columnType string) string {
	base := columnType
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	if i := strings.IndexByte(base, ' '); i >= 0 {
		base = base[:i]
	}
	return base
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func columnIndex(t *Table, name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// insertColumn place une colonne à la position demandée par
// « ADD COLUMN … AFTER x » ou « … FIRST », et non systématiquement à la fin :
// sinon chaque ORDINAL_POSITION de la table devient faux.
func insertColumn(t *Table, col *Column, pos *ast.ColumnPosition) {
	at := len(t.Columns)
	if pos != nil {
		switch pos.Tp {
		case ast.ColumnPositionFirst:
			at = 0
		case ast.ColumnPositionAfter:
			if pos.RelativeColumn != nil {
				if idx := columnIndex(t, pos.RelativeColumn.Name.O); idx >= 0 {
					at = idx + 1
				}
			}
		}
	}
	t.Columns = append(t.Columns, nil)
	copy(t.Columns[at+1:], t.Columns[at:])
	t.Columns[at] = col
}

// positionColumn déplace une colonne déjà présente, pour « MODIFY … AFTER x ».
func positionColumn(t *Table, name string, pos *ast.ColumnPosition) {
	if pos == nil || pos.Tp == ast.ColumnPositionNone {
		return
	}
	idx := columnIndex(t, name)
	if idx < 0 {
		return
	}
	col := t.Columns[idx]
	t.Columns = append(t.Columns[:idx], t.Columns[idx+1:]...)
	insertColumn(t, col, pos)
}

// dropColumn retire une colonne. Un index qui la contient n'est PAS supprimé :
// MySQL ne retire que la colonne de l'index, et ne fait disparaître l'index que
// s'il ne portait que sur elle.
func dropColumn(t *Table, name string) {
	if i := columnIndex(t, name); i >= 0 {
		t.Columns = append(t.Columns[:i], t.Columns[i+1:]...)
	}
	var kept []*Index
	for _, ix := range t.Indexes {
		remaining := ix.Columns[:0]
		for _, c := range ix.Columns {
			if !strings.EqualFold(c.Name, name) {
				remaining = append(remaining, c)
			}
		}
		ix.Columns = remaining
		if len(ix.Columns) > 0 {
			kept = append(kept, ix)
		}
	}
	t.Indexes = kept
	t.ForeignKeys = removeFK(t.ForeignKeys, func(fk *ForeignKey) bool { return containsFold(fk.Columns, name) })
}

func renameColumnRefs(t *Table, old, newName string) {
	for _, ix := range t.Indexes {
		for i := range ix.Columns {
			if strings.EqualFold(ix.Columns[i].Name, old) {
				ix.Columns[i].Name = newName
			}
		}
		if strings.EqualFold(ix.Name, old) {
			ix.Name = newName
		}
	}
	for _, fk := range t.ForeignKeys {
		for i := range fk.Columns {
			if strings.EqualFold(fk.Columns[i], old) {
				fk.Columns[i] = newName
			}
		}
	}
}

func indexHasColumn(ix *Index, name string) bool {
	for _, c := range ix.Columns {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func removeIndex(in []*Index, drop func(*Index) bool) []*Index {
	out := in[:0]
	for _, ix := range in {
		if !drop(ix) {
			out = append(out, ix)
		}
	}
	return out
}

func removeFK(in []*ForeignKey, drop func(*ForeignKey) bool) []*ForeignKey {
	out := in[:0]
	for _, fk := range in {
		if !drop(fk) {
			out = append(out, fk)
		}
	}
	return out
}

func removeCheck(in []*Check, drop func(*Check) bool) []*Check {
	out := in[:0]
	for _, c := range in {
		if !drop(c) {
			out = append(out, c)
		}
	}
	return out
}
