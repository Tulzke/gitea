// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"code.gitea.io/gitea/modules/log"
	"net/http"
	"strings"
	"sync"

	"code.gitea.io/gitea/models/db"
	"code.gitea.io/gitea/models/organization"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/markup"
	"code.gitea.io/gitea/modules/markup/markdown"
	"code.gitea.io/gitea/modules/setting"
)

const (
	tplOrgHome base.TplName = "org/home"
)

// Home show organization home page
func Home(ctx *context.Context) {
	uname := ctx.Params(":username")

	if strings.HasSuffix(uname, ".keys") || strings.HasSuffix(uname, ".gpg") {
		ctx.NotFound("", nil)
		return
	}

	ctx.SetParams(":org", uname)
	context.HandleOrgAssignment(ctx)
	if ctx.Written() {
		return
	}

	org := ctx.Org.Organization

	ctx.Data["PageIsUserProfile"] = true
	ctx.Data["Title"] = org.DisplayName()
	if len(org.Description) != 0 {
		desc, err := markdown.RenderString(&markup.RenderContext{
			Ctx:       ctx,
			URLPrefix: ctx.Repo.RepoLink,
			Metas:     map[string]string{"mode": "document"},
			GitRepo:   ctx.Repo.GitRepo,
		}, org.Description)
		if err != nil {
			ctx.ServerError("RenderString", err)
			return
		}
		ctx.Data["RenderedDescription"] = desc
	}

	var orderBy db.SearchOrderBy
	ctx.Data["SortType"] = ctx.FormString("sort")
	switch ctx.FormString("sort") {
	case "newest":
		orderBy = db.SearchOrderByNewest
	case "oldest":
		orderBy = db.SearchOrderByOldest
	case "recentupdate":
		orderBy = db.SearchOrderByRecentUpdated
	case "leastupdate":
		orderBy = db.SearchOrderByLeastUpdated
	case "reversealphabetically":
		orderBy = db.SearchOrderByAlphabeticallyReverse
	case "alphabetically":
		orderBy = db.SearchOrderByAlphabetically
	case "moststars":
		orderBy = db.SearchOrderByStarsReverse
	case "feweststars":
		orderBy = db.SearchOrderByStars
	case "mostforks":
		orderBy = db.SearchOrderByForksReverse
	case "fewestforks":
		orderBy = db.SearchOrderByForks
	default:
		ctx.Data["SortType"] = "recentupdate"
		orderBy = db.SearchOrderByRecentUpdated
	}

	keyword := ctx.FormTrim("q")
	ctx.Data["Keyword"] = keyword

	language := ctx.FormTrim("language")
	ctx.Data["Language"] = language

	page := ctx.FormInt("page")
	if page <= 0 {
		page = 1
	}

	var (
		repos []*repo_model.Repository
		count int64
		err   error
	)
	repos, count, err = repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
		ListOptions: db.ListOptions{
			PageSize: setting.UI.User.RepoPagingNum,
			Page:     page,
		},
		Keyword:            keyword,
		OwnerID:            org.ID,
		OrderBy:            orderBy,
		Private:            ctx.IsSigned,
		Actor:              ctx.Doer,
		Language:           language,
		IncludeDescription: setting.UI.SearchRepoDescription,
	})
	if err != nil {
		ctx.ServerError("SearchRepository", err)
		return
	}

	opts := &organization.FindOrgMembersOpts{
		OrgID:       org.ID,
		PublicOnly:  true,
		ListOptions: db.ListOptions{Page: 1, PageSize: 25},
	}

	if ctx.Doer != nil {
		isMember, err := org.IsOrgMember(ctx.Doer.ID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "IsOrgMember")
			return
		}
		opts.PublicOnly = !isMember && !ctx.Doer.IsAdmin
	}

	members, _, err := organization.FindOrgMembers(opts)
	if err != nil {
		ctx.ServerError("FindOrgMembers", err)
		return
	}

	membersCount, err := organization.CountOrgMembers(opts)
	if err != nil {
		ctx.ServerError("CountOrgMembers", err)
		return
	}

	repoIds := make([]int64, len(repos))
	for i, repo := range repos {
		repoIds[i] = repo.ID
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	var watchedRepoIdsMap map[int64]bool
	go func() {
		defer wg.Done()
		watchedRepoIds, err := repo_model.FilterWatchedRepoIds(ctx, ctx.Doer.ID, repoIds)
		if err != nil {
			log.Error("Failed getting watched repositories ids: %w", err)
			return
		}
		if len(watchedRepoIds) == 0 {
			return
		}
		watchedRepoIdsMap = make(map[int64]bool, len(watchedRepoIds))
		for _, id := range watchedRepoIds {
			watchedRepoIdsMap[id] = true
		}
	}()

	wg.Add(1)
	var starredRepoIdsMap map[int64]bool
	go func() {
		defer wg.Done()
		starredRepoIds, err := repo_model.FilterStarredRepoIds(ctx, ctx.Doer.ID, repoIds)
		if err != nil {
			log.Error("Failed getting starred repositories ids: %w", err)
			return
		}
		if len(starredRepoIds) == 0 {
			return
		}
		starredRepoIdsMap = make(map[int64]bool, len(starredRepoIds))
		for _, id := range starredRepoIds {
			starredRepoIdsMap[id] = true
		}
	}()

	wg.Wait()

	ctx.Data["Owner"] = org
	ctx.Data["Repos"] = repos
	ctx.Data["Total"] = count
	ctx.Data["MembersTotal"] = membersCount
	ctx.Data["Members"] = members
	ctx.Data["Teams"] = ctx.Org.Teams
	ctx.Data["DisableNewPullMirrors"] = setting.Mirror.DisableNewPull
	ctx.Data["PageIsViewRepositories"] = true
	ctx.Data["WatchedRepos"] = watchedRepoIdsMap
	ctx.Data["StarredRepos"] = starredRepoIdsMap

	pager := context.NewPagination(int(count), setting.UI.User.RepoPagingNum, page, 5)
	pager.SetDefaultParams(ctx)
	pager.AddParam(ctx, "language", "Language")
	ctx.Data["Page"] = pager
	ctx.Data["ContextUser"] = ctx.ContextUser

	ctx.HTML(http.StatusOK, tplOrgHome)
}
