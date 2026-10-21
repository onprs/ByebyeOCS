CREATE TABLE public.question_offering_price_versions (
    id uuid PRIMARY KEY,
    offering_id uuid NOT NULL REFERENCES public.question_offerings(id),
    version text NOT NULL CHECK (length(version) BETWEEN 1 AND 64),
    pricing jsonb NOT NULL CHECK (jsonb_typeof(pricing) = 'object'),
    created_by uuid REFERENCES public.admin_users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (offering_id, version),
    CHECK ((pricing->>'version' = version) IS TRUE),
    CHECK ((pricing->>'micros_per_minor' = '10000') IS TRUE)
);

INSERT INTO public.question_offering_price_versions(id,offering_id,version,pricing)
SELECT gen_random_uuid(),id,pricing->>'version',pricing
FROM public.question_offerings WHERE pricing IS NOT NULL;

CREATE FUNCTION public.protect_question_price_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'question price versions are immutable';
END;
$$;
CREATE TRIGGER question_price_versions_immutable
BEFORE UPDATE OR DELETE ON public.question_offering_price_versions
FOR EACH ROW EXECUTE FUNCTION public.protect_question_price_version();
CREATE TRIGGER question_price_versions_no_truncate
BEFORE TRUNCATE ON public.question_offering_price_versions
FOR EACH STATEMENT EXECUTE FUNCTION public.protect_question_price_version();
