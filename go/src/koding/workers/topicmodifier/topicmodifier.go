package topicmodifier

import (
	"fmt"
	. "koding/db/models"
	helper "koding/db/mongodb/modelhelper"
	"strings"
)

var (
)

//Deletes given tags. Tags are removed from post bodies and collections.
//Tag relations are also removed.
func deleteTags(tagId string) error {
	log.Info("Deleting topic")
	tag, err := helper.GetTagById(tagId)
	if err != nil {
		log.Error("Tag not found - Id: ", tagId)
		return err
	}
	log.Info("Deleting %s", tag.Title)
	selector := helper.Selector{"targetId": helper.GetObjectId(tagId), "as": "tag"}

	rels, err := helper.GetRelationships(selector)
	// remove panic
	if err != nil {
		return err
	}

	updatePosts(rels, "")

	err = updateTagRelationships(rels, &Tag{})
	if err != nil {
		return err
	}
	postRels := convertTagRelationships(rels)
	err = updateTagRelationships(postRels, &Tag{})
	if err != nil {
		return err
	}
	tag.Counts = TagCount{}
	return helper.UpdateTag(tag)
}

func mergeTags(tagId string) error {
	log.Info("Merging topics")

	tag, err := helper.GetTagById(tagId)
	if err != nil {
		log.Error("Tag not found - Id: ", tagId)
		return err
	}

	synonym, err := FindSynonym(tagId)
	if err != nil {
		log.Error("Synonym not found - Id %s", tagId)
		return err
	}

	log.Info("Merging Topic %s into %s", tag.Title, synonym.Title)

	selector := helper.Selector{"targetId": helper.GetObjectId(tagId), "as": "tag"}
	tagRels, err := helper.GetRelationships(selector)
	// remove panic
	if err != nil {
		return err
	}

	taggedPostCount := len(tagRels)
	log.Info("%v tagged posts found", taggedPostCount)
	if taggedPostCount > 0 {
		updatedPostRels := updatePosts(tagRels, synonym.Id.Hex())
		postCount := len(updatedPostRels)
		log.Info("Merged Post count %d", postCount)
		synonym.Counts.Post += postCount

		updateTagRelationships(updatedPostRels, synonym)
		postRels := convertTagRelationships(updatedPostRels)
		updateTagRelationships(postRels, synonym)
	}

	updateCounts(tag, synonym)

	count, err := updateFollowers(tag, synonym)
	if err != nil {
		return err
	}
	synonym.Counts.Followers += count
	err = helper.UpdateTag(synonym)
	if err != nil {
		return err
	}

	tag.Counts = TagCount{} // reset counts
	return helper.UpdateTag(tag)

}

func convertTagRelationships(tagRels []Relationship) (postRelationships []Relationship) {
	for _, tagRel := range tagRels {
		postRelationships = append(postRelationships, swapTagRelation(&tagRel, "post"))
	}

	return postRelationships
}

//Update post tags with new ones. When newTagId = "" or post already
//includes new tag, then it just removes old tag and also removes tag relationship
//Returns Filtered Relationships
func updatePosts(rels []Relationship, newTagId string) (filteredRels []Relationship) {
	for _, rel := range rels {
		tagId := rel.TargetId.Hex()
		post, err := helper.GetStatusUpdateById(rel.SourceId.Hex())
		if err != nil {
			log.Error("Status Update Not Found - Id: %s, Err: %s", rel.SourceId.Hex(), err)
			continue
		}

		tagIncluded := updatePostBody(post, tagId, newTagId)
		if strings.TrimSpace(post.Body) == "" {
			DeleteStatusUpdate(post.Id.Hex())
		} else {
			err = helper.UpdateStatusUpdate(post)
		}

		if err != nil {
			log.Error(err.Error())
			continue
		}

		if !tagIncluded {
			filteredRels = append(filteredRels, rel)
		} else {
			RemoveRelationship(&rel)
			postRel := swapTagRelation(&rel, "post")
			RemoveRelationship(&postRel)
		}
	}

	return filteredRels
}

//Replaces given post tagId with new one. If new tag is already included
//then it just removes old one.
//Returns tag included information
func updatePostBody(s *StatusUpdate, tagId string, newTagId string) (tagIncluded bool) {
	var newTag string
	tagIncluded = false
	if newTagId != "" {
		newTag = fmt.Sprintf("|#:JTag:%v|", newTagId)
		//new tag already included in post
		if strings.Index(s.Body, newTag) != -1 {
			tagIncluded = true
			newTag = ""
		}
	}

	modifiedTag := fmt.Sprintf("|#:JTag:%v|", tagId)
	s.Body = strings.Replace(s.Body, modifiedTag, newTag, -1)
	return tagIncluded
}

//Removes old tag relationships and creates new ones if synonym tag does exists
func updateTagRelationships(rels []Relationship, synonym *Tag) error {
	for _, rel := range rels {
		err := RemoveRelationship(&rel)
		if err != nil {
			return err
		}
		if synonym.Id.Hex() != "" {
			if rel.TargetName == "JTag" {
				rel.TargetId = synonym.Id
			} else {
				rel.SourceId = synonym.Id
			}
			rel.Id = helper.NewObjectId()
			err = CreateRelationship(&rel)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func updateCounts(tag *Tag, synonym *Tag) {
	synonym.Counts.Following += tag.Counts.Following // does this have any meaning?
	synonym.Counts.Tagged += tag.Counts.Tagged
}

//Moves follower information under the new topic. If user is already following
//new topic, then she is not added as follower.
func updateFollowers(tag *Tag, synonym *Tag) (int, error) {
	selector := helper.Selector{
		"sourceId":   tag.Id,
		"as":         "follower",
		"targetName": "JAccount",
	}

	rels, err := helper.GetRelationships(selector)
	// remove panic
	if err != nil {
		return 0, err
	}

	var oldFollowers []Relationship
	var newFollowers []Relationship

	for _, rel := range rels {
		selector["sourceId"] = synonym.Id
		selector["targetId"] = rel.TargetId

		// checking if relationship already exists for the synonym
		_, err := helper.GetRelationship(selector)
		//because there are two relations as account -> follower -> tag and
		//tag -> follower -> account, we have added
		if err != nil {
			if err == mgo.ErrNotFound {
				newFollowers = append(newFollowers, rel)
			} else {
				log.Error(err.Error())
				return 0, err
			}
		} else {
			oldFollowers = append(oldFollowers, rel)
		}
	}

	log.Info("%v users are already following new topic", len(oldFollowers))
	if len(oldFollowers) > 0 {
		err = updateTagRelationships(oldFollowers, &Tag{})
		if err != nil {
			return 0, err
		}
	}
	log.Info("%v users followed new topic", len(newFollowers))
	if len(newFollowers) > 0 {
		err = updateTagRelationships(newFollowers, synonym)
		if err != nil {
			return 0, err
		}
	}

	return len(newFollowers), nil
}

func swapTagRelation(r *Relationship, as string) Relationship {
	return Relationship{
		As:         as,
		SourceId:   r.TargetId,
		SourceName: r.TargetName,
		TargetId:   r.SourceId,
		TargetName: r.SourceName,
		TimeStamp:  r.TimeStamp,
	}
}
