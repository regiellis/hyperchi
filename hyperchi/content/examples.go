package content

import (
	"errors"
	"strings"
	"time"
)

// The schemas below cover common content types. Use one as is, or copy it as
// a starting point for your own.

// BlogPostSchema returns a schema for blog posts. Title and author are
// required, a published post needs publishedAt, and a missing excerpt is
// filled from the first 200 characters of the body.
func BlogPostSchema() *ContentSchema {
	return NewSchema("BlogPost").
		Field("title", FieldTypeString).
		Required().
		MinLength(3).
		MaxLength(200).
		Description("Post title").
		Field("excerpt", FieldTypeString).
		MaxLength(500).
		Description("Brief description of the post").
		Field("published", FieldTypeBool).
		Default(false).
		Description("Whether the post is published").
		Field("publishedAt", FieldTypeTime).
		Description("When the post was published").
		Field("tags", FieldTypeSlice).
		Description("Post tags").
		Field("category", FieldTypeString).
		Description("Post category").
		Field("author", FieldTypeString).
		Required().
		Description("Post author").
		Build().
		Required("title", "author").
		Validate(func(content *Content) error {
			if content.GetBool("published") {
				if content.GetTime("publishedAt").IsZero() {
					return errors.New("published posts must have a publishedAt date")
				}
			}
			return nil
		}).
		Transform(func(content *Content) *Content {
			// Cut on characters, not bytes, so multi-byte text is neither
			// shortened nor split mid-rune.
			if runes := []rune(content.Body); !content.Has("excerpt") && len(runes) > 200 {
				content.Set("excerpt", string(runes[:200])+"...")
			}
			return content
		}).
		Build()
}

// ProductSchema returns a schema for e-commerce products.
func ProductSchema() *ContentSchema {
	return NewSchema("Product").
		Field("name", FieldTypeString).
		Required().
		MinLength(2).
		MaxLength(100).
		Description("Product name").
		Field("price", FieldTypeFloat).
		Required().
		Min(0.0).
		Description("Product price").
		Field("sku", FieldTypeString).
		Required().
		Description("Product SKU").
		Validate(func(value any) error {
			if str, ok := value.(string); ok {
				if len(str) < 3 {
					return errors.New("SKU must be at least 3 characters")
				}
				if !strings.Contains(str, "-") {
					return errors.New("SKU must contain a hyphen")
				}
			}
			return nil
		}).
		Field("inStock", FieldTypeBool).
		Default(true).
		Description("Whether product is in stock").
		Field("categories", FieldTypeSlice).
		Description("Product categories").
		Field("specifications", FieldTypeMap).
		Description("Product specifications").
		Field("rating", FieldTypeFloat).
		Min(0.0).
		Max(5.0).
		Description("Product rating").
		Build().
		Required("name", "price", "sku").
		Build()
}

// DocumentationSchema returns a schema for documentation pages.
func DocumentationSchema() *ContentSchema {
	return NewSchema("Documentation").
		Field("title", FieldTypeString).
		Required().
		MinLength(5).
		MaxLength(150).
		Description("Page title").
		Field("section", FieldTypeString).
		Required().
		Description("Documentation section").
		Field("order", FieldTypeInt).
		Default(0).
		Min(0).
		Description("Order within section").
		Field("level", FieldTypeString).
		Default("beginner").
		Description("Difficulty level").
		Validate(func(value any) error {
			if str, ok := value.(string); ok {
				validLevels := []string{"beginner", "intermediate", "advanced"}
				for _, level := range validLevels {
					if str == level {
						return nil
					}
				}
				return errors.New("level must be one of: beginner, intermediate, advanced")
			}
			return nil
		}).
		Field("version", FieldTypeString).
		Description("Version this applies to").
		Field("keywords", FieldTypeSlice).
		Description("Keywords for search").
		Field("relatedPages", FieldTypeSlice).
		Description("Related documentation pages").
		Build().
		Required("title", "section").
		Build()
}

// ProjectSchema returns a schema for portfolio projects. An endDate before
// startDate fails validation.
func ProjectSchema() *ContentSchema {
	return NewSchema("Project").
		Field("name", FieldTypeString).
		Required().
		MinLength(2).
		MaxLength(100).
		Description("Project name").
		Field("description", FieldTypeString).
		Required().
		MinLength(10).
		MaxLength(500).
		Description("Project description").
		Field("status", FieldTypeString).
		Default("in-progress").
		Description("Project status").
		Validate(func(value any) error {
			if str, ok := value.(string); ok {
				validStatuses := []string{"planning", "in-progress", "completed", "archived"}
				for _, status := range validStatuses {
					if str == status {
						return nil
					}
				}
				return errors.New("status must be one of: planning, in-progress, completed, archived")
			}
			return nil
		}).
		Field("technologies", FieldTypeSlice).
		Description("Technologies used").
		Field("githubUrl", FieldTypeString).
		Description("GitHub repository URL").
		Field("demoUrl", FieldTypeString).
		Description("Live demo URL").
		Field("featured", FieldTypeBool).
		Default(false).
		Description("Whether project is featured").
		Field("startDate", FieldTypeTime).
		Description("Project start date").
		Field("endDate", FieldTypeTime).
		Description("Project end date").
		Build().
		Required("name", "description").
		Validate(func(content *Content) error {
			startDate := content.GetTime("startDate")
			endDate := content.GetTime("endDate")
			if !startDate.IsZero() && !endDate.IsZero() && endDate.Before(startDate) {
				return errors.New("end date must be after start date")
			}
			return nil
		}).
		Build()
}

// EventSchema returns a schema for events. endTime must not be before
// startTime, and an event whose startTime has passed fails validation, so
// past events stop loading once they start.
func EventSchema() *ContentSchema {
	return NewSchema("Event").
		Field("title", FieldTypeString).
		Required().
		MinLength(5).
		MaxLength(200).
		Description("Event title").
		Field("startTime", FieldTypeTime).
		Required().
		Description("Event start time").
		Field("endTime", FieldTypeTime).
		Required().
		Description("Event end time").
		Field("location", FieldTypeString).
		Description("Event location").
		Field("capacity", FieldTypeInt).
		Min(1).
		Description("Maximum attendees").
		Field("registrationRequired", FieldTypeBool).
		Default(false).
		Description("Whether registration is required").
		Field("tags", FieldTypeSlice).
		Description("Event tags").
		Field("organizer", FieldTypeString).
		Required().
		Description("Event organizer").
		Build().
		Required("title", "startTime", "endTime", "organizer").
		Validate(func(content *Content) error {
			startTime := content.GetTime("startTime")
			endTime := content.GetTime("endTime")
			if endTime.Before(startTime) {
				return errors.New("end time must be after start time")
			}

			if startTime.Before(time.Now()) {
				return errors.New("events must be scheduled for the future")
			}

			return nil
		}).
		Build()
}

// RecipeSchema returns a schema for recipes. Ingredients and instructions
// must each have at least one entry.
func RecipeSchema() *ContentSchema {
	return NewSchema("Recipe").
		Field("title", FieldTypeString).
		Required().
		MinLength(3).
		MaxLength(150).
		Description("Recipe title").
		Field("prepTime", FieldTypeInt).
		Required().
		Min(1).
		Description("Preparation time in minutes").
		Field("cookTime", FieldTypeInt).
		Min(0).
		Description("Cooking time in minutes").
		Field("servings", FieldTypeInt).
		Required().
		Min(1).
		Max(50).
		Description("Number of servings").
		Field("difficulty", FieldTypeString).
		Default("easy").
		Description("Recipe difficulty").
		Validate(func(value any) error {
			if str, ok := value.(string); ok {
				validDifficulties := []string{"easy", "medium", "hard"}
				for _, difficulty := range validDifficulties {
					if str == difficulty {
						return nil
					}
				}
				return errors.New("difficulty must be one of: easy, medium, hard")
			}
			return nil
		}).
		Field("ingredients", FieldTypeSlice).
		Required().
		Description("Recipe ingredients").
		Field("instructions", FieldTypeSlice).
		Required().
		Description("Cooking instructions").
		Field("cuisine", FieldTypeString).
		Description("Cuisine type").
		Field("dietary", FieldTypeSlice).
		Description("Dietary restrictions/labels").
		Build().
		Required("title", "prepTime", "servings", "ingredients", "instructions").
		Validate(func(content *Content) error {
			ingredients := content.GetSlice("ingredients")
			if len(ingredients) == 0 {
				return errors.New("recipe must have at least one ingredient")
			}

			instructions := content.GetSlice("instructions")
			if len(instructions) == 0 {
				return errors.New("recipe must have at least one instruction")
			}

			return nil
		}).
		Build()
}

// ExampleBlogSetup returns an empty Markdown blog collection using
// BlogPostSchema.
func ExampleBlogSetup() *Collection {
	schema := BlogPostSchema()

	config := CollectionConfig{
		Name:   "blog",
		Path:   "content/blog",
		Schema: schema,
		Type:   ContentTypeMarkdown,
	}

	return NewCollection(config)
}

// ExampleProductSetup returns an empty Markdown product collection using
// ProductSchema.
func ExampleProductSetup() *Collection {
	schema := ProductSchema()

	config := CollectionConfig{
		Name:   "products",
		Path:   "content/products",
		Schema: schema,
		Type:   ContentTypeMarkdown,
	}

	return NewCollection(config)
}

// ExampleDocsSetup returns an empty MDX docs collection using
// DocumentationSchema.
func ExampleDocsSetup() *Collection {
	schema := DocumentationSchema()

	config := CollectionConfig{
		Name:   "docs",
		Path:   "content/docs",
		Schema: schema,
		Type:   ContentTypeMDX,
	}

	return NewCollection(config)
}

// ExampleCompleteSetup returns a layer with the blog, products, docs, and
// projects collections declared. It uses DefaultContentConfig, so LoadContent
// also starts a watcher; stop it with Close.
func ExampleCompleteSetup() *ContentLayer {
	contentLayer := NewContentLayer(DefaultContentConfig())

	contentLayer.DefineCollection(CollectionConfig{
		Name:   "blog",
		Path:   "content/blog",
		Schema: BlogPostSchema(),
		Type:   ContentTypeMarkdown,
	})

	contentLayer.DefineCollection(CollectionConfig{
		Name:   "products",
		Path:   "content/products",
		Schema: ProductSchema(),
		Type:   ContentTypeMarkdown,
	})

	contentLayer.DefineCollection(CollectionConfig{
		Name:   "docs",
		Path:   "content/docs",
		Schema: DocumentationSchema(),
		Type:   ContentTypeMDX,
	})

	contentLayer.DefineCollection(CollectionConfig{
		Name:   "projects",
		Path:   "content/projects",
		Schema: ProjectSchema(),
		Type:   ContentTypeMarkdown,
	})

	return contentLayer
}
