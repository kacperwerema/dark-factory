package tracker

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fillValue sets v, and everything under it that is exported, to a non-zero
// value derived from tag, so two fills with different tags differ in every
// leaf. depth stops a type that contains itself.
func fillValue(t *testing.T, v reflect.Value, tag string, depth int) {
	t.Helper()
	if depth > 6 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(tag)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(len(tag)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(len(tag)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(len(tag)) + 0.5)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillValue(t, p.Elem(), tag, depth+1)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillValue(t, s.Index(0), tag, depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fillValue(t, k, tag, depth+1)
		e := reflect.New(v.Type().Elem()).Elem()
		fillValue(t, e, tag, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillValue(t, v.Field(i), tag, depth+1)
			}
		}
	case reflect.Func:
		v.Set(reflect.MakeFunc(v.Type(), func(args []reflect.Value) []reflect.Value {
			out := make([]reflect.Value, v.Type().NumOut())
			for i := range out {
				out[i] = reflect.Zero(v.Type().Out(i))
			}
			return out
		}))
	case reflect.Interface:
		if reflect.TypeOf(errors.New("")).Implements(v.Type()) {
			v.Set(reflect.ValueOf(errors.New(tag)))
			return
		}
		t.Fatalf("fillValue: no filler for interface %s (%s); add one", v.Type(), tag)
	default:
		t.Fatalf("fillValue: no filler for kind %s (%s); add one", v.Kind(), tag)
	}
}

// filledWorkflow is a WorkflowConfig with every exported field non-zero,
// each leaf derived from prefix and the field's name.
func filledWorkflow(t *testing.T, prefix string) *WorkflowConfig {
	t.Helper()
	w := &WorkflowConfig{}
	v := reflect.ValueOf(w).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		fillValue(t, v.Field(i), prefix+"-"+f.Name, 0)
		if v.Field(i).IsZero() {
			t.Fatalf("filledWorkflow left %s zero", f.Name)
		}
	}
	return w
}

// sameField is whether a and b hold the same value: funcs by identity
// (reflect.DeepEqual never calls two non-nil funcs equal), the rest deeply.
func sameField(a, b reflect.Value) bool {
	if a.Kind() == reflect.Func {
		return a.IsNil() == b.IsNil() && a.Pointer() == b.Pointer()
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// TestCloneCopiesEveryField: Clone lists fields by hand, so a field added
// to WorkflowConfig without a line there vanished from every ForIssue,
// ForType and ForSystem result (Memory, Privacy, Decisions did). Every
// exported field must come through, equal to the original's.
func TestCloneCopiesEveryField(t *testing.T) {
	orig := filledWorkflow(t, "orig")
	clone := orig.Clone()
	ov, cv := reflect.ValueOf(orig).Elem(), reflect.ValueOf(clone).Elem()
	var missing, differ []string
	for i := 0; i < ov.NumField(); i++ {
		f := ov.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		switch {
		case cv.Field(i).IsZero():
			missing = append(missing, f.Name)
		case !sameField(ov.Field(i), cv.Field(i)):
			differ = append(differ, f.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("Clone (internal/tracker/workflow_merge.go) leaves these fields zero; copy them: %s", strings.Join(missing, ", "))
	}
	if len(differ) > 0 {
		t.Errorf("Clone copies these fields with a different value: %s", strings.Join(differ, ", "))
	}
}

// TestCloneIsIndependent: a slice, map or pointer block in the clone is
// its own copy, so scoping an issue's config never edits the base.
func TestCloneIsIndependent(t *testing.T) {
	orig := filledWorkflow(t, "orig")
	clone := orig.Clone()
	clone.Statuses[0].Name = "changed"
	clone.Features.Installed[0] = "changed"
	clone.Systems["orig-Systems"].Statuses[0].Name = "changed"
	*clone.WorktreeSparseExclude = append(*clone.WorktreeSparseExclude, "changed")
	if orig.Statuses[0].Name == "changed" || orig.Features.Installed[0] == "changed" ||
		orig.Systems["orig-Systems"].Statuses[0].Name == "changed" || len(*orig.WorktreeSparseExclude) != 1 {
		t.Fatal("editing the clone changed the original")
	}
}

// yamlKey is a field's workflow.yaml key ("" for a runtime-only field).
func yamlKey(f reflect.StructField) string {
	k, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if k == "-" {
		return ""
	}
	return k
}

// TestDepartmentWorkflowCopiesEveryField: a department's workflow is its
// own file's, field for field, except ProjectWideKeys, which come from
// Product's file. The playtest runner and worktree settings fall back to
// Product's only when the department file names none.
func TestDepartmentWorkflowCopiesEveryField(t *testing.T) {
	projectWide := map[string]bool{}
	for _, k := range ProjectWideKeys {
		projectWide[k] = true
	}
	fallback := map[string]bool{"PlaytestCommand": true, "PlaytestReport": true,
		"Worktree": true, "WorktreeSetup": true, "WorktreeSparseExclude": true}

	check := func(t *testing.T, own, product, got *WorkflowConfig, fromProduct func(reflect.StructField) bool) {
		t.Helper()
		gv, ov, pv := reflect.ValueOf(got).Elem(), reflect.ValueOf(own).Elem(), reflect.ValueOf(product).Elem()
		seen := map[string]bool{}
		for i := 0; i < gv.NumField(); i++ {
			f := gv.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			if k := yamlKey(f); projectWide[k] {
				seen[k] = true
			}
			want, from := ov.Field(i), "the department file"
			if fromProduct(f) {
				want, from = pv.Field(i), "Product's file"
			}
			if gv.Field(i).IsZero() {
				t.Errorf("%s is zero; want it from %s", f.Name, from)
			} else if !sameField(gv.Field(i), want) {
				t.Errorf("%s does not come from %s", f.Name, from)
			}
		}
		for _, k := range ProjectWideKeys {
			if !seen[k] {
				t.Errorf("ProjectWideKeys names %q, which no WorkflowConfig field has as its yaml key", k)
			}
		}
	}

	t.Run("department file names everything", func(t *testing.T) {
		own, product := filledWorkflow(t, "own"), filledWorkflow(t, "product")
		check(t, own, product, departmentWorkflow(own, product), func(f reflect.StructField) bool {
			return projectWide[yamlKey(f)]
		})
	})

	t.Run("department file names no runner or worktree", func(t *testing.T) {
		own, product := filledWorkflow(t, "own"), filledWorkflow(t, "product")
		own.PlaytestCommand, own.PlaytestReport, own.Worktree = "", "", nil
		got := departmentWorkflow(own, product)
		check(t, own, product, got, func(f reflect.StructField) bool {
			return projectWide[yamlKey(f)] || fallback[f.Name]
		})
	})
}

func ExampleWorkflowConfig_Clone() {
	w := &WorkflowConfig{Memory: &MemoryConfig{}}
	fmt.Println(w.Clone().Memory != nil)
	// Output: true
}
