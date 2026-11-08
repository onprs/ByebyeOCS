-- 任务点可用性属于稳定任务点身份，不能随课程快照版本重建而丢失。
-- 先回填现有附件上已探测到的状态，再供课程与任务读取。
CREATE TABLE public.learning_task_point_availability (
    user_id uuid NOT NULL,
    course_id uuid NOT NULL,
    chapter_external_id text NOT NULL,
    task_point_id text NOT NULL,
    cycle_key text NOT NULL DEFAULT 'current',
    availability text NOT NULL DEFAULT 'unknown'
        CHECK (availability IN ('unknown', 'open', 'closed')),
    checked_at timestamp with time zone,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, course_id, chapter_external_id, task_point_id, cycle_key),
    FOREIGN KEY (course_id, user_id) REFERENCES public.learning_courses (id, user_id) ON DELETE CASCADE
);

CREATE INDEX learning_task_point_availability_status_idx
    ON public.learning_task_point_availability (user_id, course_id, availability);

INSERT INTO public.learning_task_point_availability
    (user_id, course_id, chapter_external_id, task_point_id, availability, checked_at, updated_at)
SELECT DISTINCT ON (a.user_id, a.course_id, ch.external_id, a.task_point_id)
    a.user_id, a.course_id, ch.external_id, a.task_point_id,
    a.availability, a.availability_checked_at, now()
FROM public.learning_attachments a
JOIN public.learning_chapters ch ON ch.id = a.chapter_id AND ch.user_id = a.user_id
WHERE a.is_task_point AND a.availability <> 'unknown'
ORDER BY a.user_id, a.course_id, ch.external_id, a.task_point_id,
         a.availability_checked_at DESC NULLS LAST;
