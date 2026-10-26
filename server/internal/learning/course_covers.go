package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const courseCoverTTL = 24 * time.Hour
const courseCoverFailureTTL = 5 * time.Minute
const courseCoverCacheLimit = 1024
const courseCoverByteLimit = 256 << 10

var errCoverUnavailable = apiErr(404, "cover_unavailable", "课程封面暂不可用")

type courseCover struct {
	Bytes []byte
	Hash  string
}

func coverSourceHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func courseCoverURL(courseID, source string) string {
	if source == "" {
		return ""
	}
	return "courses/" + courseID + "/cover?v=" + coverSourceHash(source)
}

// loadCourseCover 返回找到的负缓存时Bytes为空，避免图片失败造成持续回源。
func (s *Service) loadCourseCover(ctx context.Context, sourceHash string) (courseCover, bool, error) {
	var image courseCover
	err := s.pool.QueryRow(ctx, `SELECT content,content_hash FROM public.learning_course_cover_cache
		WHERE source_hash=$1 AND expires_at>$2`, sourceHash, s.now()).Scan(&image.Bytes, &image.Hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return courseCover{}, false, nil
	}
	if err != nil {
		return courseCover{}, false, errServiceUnavailable
	}
	return image, true, nil
}

func (s *Service) storeCourseCover(ctx context.Context, sourceHash string, cover courseCover) error {
	ttl := courseCoverTTL
	if len(cover.Bytes) == 0 {
		ttl = courseCoverFailureTTL
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		// 写入与全局条目上限共享事务锁；图片网络读取发生在事务外。
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(726239184026)`); err != nil {
			return errServiceUnavailable
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.learning_course_cover_cache
			(source_hash,content,content_hash,expires_at,updated_at) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (source_hash) DO UPDATE SET content=EXCLUDED.content,
			content_hash=EXCLUDED.content_hash,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`,
			sourceHash, cover.Bytes, cover.Hash, s.now().Add(ttl), s.now()); err != nil {
			return errServiceUnavailable
		}
		_, err := tx.Exec(ctx, `DELETE FROM public.learning_course_cover_cache WHERE source_hash IN (
			SELECT source_hash FROM public.learning_course_cover_cache
			ORDER BY updated_at DESC,source_hash DESC OFFSET $1)`, courseCoverCacheLimit)
		return err
	})
}

// GetCourseCover 每次先确认课程归属，缓存仅复用无会话的公共图片内容。
func (s *Service) GetCourseCover(ctx context.Context, actor Actor, courseID, version string) (courseCover, error) {
	if !web.ValidID(courseID) {
		return courseCover{}, errNotFound
	}
	var source string
	err := s.pool.QueryRow(ctx, `SELECT cover_source_url FROM public.learning_courses
		WHERE id=$1 AND user_id=$2`, courseID, actor.ID).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && source == "" {
		return courseCover{}, errNotFound
	}
	if err != nil {
		return courseCover{}, errServiceUnavailable
	}
	hash := coverSourceHash(source)
	if version != "" && version != hash {
		return courseCover{}, errNotFound
	}
	cached, found, err := s.loadCourseCover(ctx, hash)
	if err != nil {
		return courseCover{}, err
	}
	if found {
		if len(cached.Bytes) == 0 {
			return courseCover{}, errCoverUnavailable
		}
		return cached, nil
	}
	// 同一图片只进行一次有界回源；某个浏览器取消图片加载不会取消其他用户共用的读取。
	result := s.coverFlights.DoChan(hash, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if cached, found, err := s.loadCourseCover(fetchCtx, hash); err != nil {
			return courseCover{}, err
		} else if found {
			if len(cached.Bytes) == 0 {
				return courseCover{}, errCoverUnavailable
			}
			return cached, nil
		}
		platform, ok := s.opts.Platform.(CourseCoverPlatform)
		if !ok {
			return courseCover{}, errCoverUnavailable
		}
		image, err := platform.FetchCourseCover(fetchCtx, source)
		if err != nil || len(image.Bytes) == 0 || len(image.Bytes) > courseCoverByteLimit {
			_ = s.storeCourseCover(fetchCtx, hash, courseCover{})
			return courseCover{}, errCoverUnavailable
		}
		cover := courseCover{Bytes: image.Bytes, Hash: coverSourceHash(string(image.Bytes))}
		// 图片已经读取成功时，缓存写入失败不改变本次展示结果。
		_ = s.storeCourseCover(fetchCtx, hash, cover)
		return cover, nil
	})
	select {
	case <-ctx.Done():
		return courseCover{}, errCoverUnavailable
	case value := <-result:
		if value.Err != nil {
			return courseCover{}, value.Err
		}
		return value.Val.(courseCover), nil
	}
}

func (s *Service) handleCourseCover(w http.ResponseWriter, r *http.Request, actor Actor) {
	cover, err := s.GetCourseCover(r.Context(), actor, r.PathValue("id"), r.URL.Query().Get("v"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	etag := `"` + cover.Hash + `"`
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Add("Vary", "Cookie, Authorization")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cover.Bytes)
}
