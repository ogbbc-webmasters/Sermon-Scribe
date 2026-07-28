module Editor exposing (Effect(..), Model, Msg(..), init, pipeline, update, view)

import Api exposing (Region, Sermon, Timeline, Waveform)
import Html exposing (Html, audio, button, canvas, div, h1, node, p, span, strong, text)
import Html.Attributes exposing (attribute, class, controls, disabled, id, src, tabindex, title)
import Html.Events exposing (onClick)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Timeline
import Ui


type alias Model =
    { sermon : Sermon
    , waveform : Maybe Waveform
    , analysis : Maybe Timeline
    , draft : Maybe Timeline
    , draftReceived : Bool
    , regions : List Region
    , selected : Int
    , dirty : Bool
    , applying : Bool
    , finalReady : Bool
    , confirmingApproval : Bool
    , error : Maybe String
    , backWarning : Bool
    , playhead : Float
    , zoom : Float
    , viewStart : Float
    , nudgeStep : Float
    }


type Msg
    = GotWaveform (Result Http.Error Waveform)
    | GotAnalysis (Result Http.Error Timeline)
    | GotDraft Decode.Value
    | Select Int
    | Toggle Int
    | SetKeep Int Bool
    | Nudge Int Float
    | CanvasSelect Decode.Value
    | Playhead Decode.Value
    | ZoomIn
    | ZoomOut
    | Pan Float
    | ShowAll
    | SetNudgeStep Float
    | SelectPrevious
    | SelectNext
    | Apply
    | Applied (Result Http.Error Sermon)
    | ConfirmApproval
    | CancelApproval
    | Approve
    | Approved (Result Http.Error Sermon)
    | Back
    | ForceClose
    | KeepEditing


type Effect
    = None
    | LoadDraft String
    | SaveDraft String Encode.Value Encode.Value Encode.Value
    | ClearDraft String
    | Render Encode.Value Encode.Value
    | Complete String Encode.Value Encode.Value
    | Preview Encode.Value
    | Close
    | ApprovedSermon Sermon


init : Sermon -> ( Model, Cmd Msg, Effect )
init sermon =
    ( { sermon = sermon, waveform = Nothing, analysis = Nothing, draft = Nothing, draftReceived = False, regions = [], selected = 0, dirty = False, applying = False, finalReady = sermon.stage == "edit" && sermon.status == "done", confirmingApproval = False, error = Nothing, backWarning = False, playhead = 0, zoom = 1, viewStart = 0, nudgeStep = 0.01 }
    , Cmd.batch [ Api.waveform GotWaveform sermon.id, Api.analyze GotAnalysis sermon.id ]
    , LoadDraft sermon.id
    )


resolve : Model -> Model
resolve model =
    case ( model.waveform, model.analysis, model.draftReceived ) of
        ( Just wave, Just analyzed, True ) ->
            let
                validDraft =
                    model.draft
                        |> Maybe.andThen
                            (\draft ->
                                if abs (draft.duration - wave.duration) < 0.01 && valid wave.duration draft.regions then
                                    Just draft.regions

                                else
                                    Nothing
                            )

                chosen =
                    validDraft
                        |> Maybe.withDefault
                            (if model.finalReady then
                                model.sermon.appliedRegions |> Maybe.withDefault analyzed.regions

                             else
                                analyzed.regions
                            )
            in
            if List.isEmpty model.regions then
                { model | regions = chosen, dirty = validDraft /= Nothing, finalReady = model.finalReady && validDraft == Nothing }

            else
                model

        _ ->
            model


update : Msg -> Model -> ( Model, Cmd Msg, Effect )
update msg model =
    case msg of
        GotWaveform result ->
            case result of
                Ok wave ->
                    finish { model | waveform = Just wave }

                Err _ ->
                    ( { model | error = Just "Could not load the waveform. Please return and try again." }, Cmd.none, None )

        GotAnalysis result ->
            case result of
                Ok value ->
                    finish { model | analysis = Just value }

                Err _ ->
                    ( { model | error = Just "Could not analyze this recording. Please try again." }, Cmd.none, None )

        GotDraft value ->
            case Decode.decodeValue (Decode.map2 Tuple.pair (Decode.field "id" Decode.string) (Decode.field "draft" Decode.value)) value of
                Ok ( id, draftValue ) ->
                    if id == model.sermon.id then
                        case Decode.decodeValue (Decode.nullable Timeline.decoder) draftValue of
                            Ok draft ->
                                finish { model | draftReceived = True, draft = draft }

                            Err _ ->
                                finishDiscardDraft { model | draftReceived = True, draft = Nothing }

                    else
                        ( model, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        Select index ->
            redraw (selectRegion index model)

        Toggle index ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changed
                    { model
                        | regions =
                            List.indexedMap
                                (\i r ->
                                    if i == index then
                                        { r | keep = not r.keep }

                                    else
                                        r
                                )
                                model.regions
                        , selected = index
                    }

        SetKeep index keep ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changed
                    { model
                        | regions =
                            List.indexedMap
                                (\i region ->
                                    if i == index then
                                        { region | keep = keep }

                                    else
                                        region
                                )
                                model.regions
                        , selected = index
                    }

        Nudge boundary delta ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changeBoundary boundary (Timeline.boundaryTime boundary model.regions + delta) model

        CanvasSelect value ->
            case Decode.decodeValue Decode.int value of
                Ok index ->
                    redraw (selectRegion index model)

                Err _ ->
                    ( model, Cmd.none, None )

        Playhead value ->
            case Decode.decodeValue Decode.float value of
                Ok time ->
                    ( { model | playhead = time }, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        ZoomIn ->
            redraw (setZoom (model.zoom * 2) model)

        ZoomOut ->
            redraw (setZoom (model.zoom / 2) model)

        Pan direction ->
            let
                span =
                    visibleSpan model

                next =
                    { model | viewStart = clampViewStart model (model.viewStart + direction * span * 0.75) }
            in
            redraw next

        ShowAll ->
            redraw { model | zoom = 1, viewStart = 0 }

        SetNudgeStep step ->
            ( { model | nudgeStep = step }, Cmd.none, None )

        SelectPrevious ->
            redraw (selectRegion (model.selected - 1) model)

        SelectNext ->
            redraw (selectRegion (model.selected + 1) model)

        Apply ->
            if busy model then
                ( model, Cmd.none, None )

            else
                ( { model | applying = True, error = Nothing }, Api.applyEdits Applied model.sermon.id model.regions, None )

        Applied result ->
            case result of
                Ok _ ->
                    -- SSE snapshots and updates are authoritative. The queue can
                    -- finish before this older HTTP acknowledgement arrives.
                    ( model, Cmd.none, None )

                Err _ ->
                    ( { model | applying = False, error = Just "Could not queue the edit. Check the regions and try again." }, Cmd.none, None )

        ConfirmApproval ->
            ( { model | confirmingApproval = True }, Cmd.none, None )

        CancelApproval ->
            ( { model | confirmingApproval = False }, Cmd.none, None )

        Approve ->
            ( { model | applying = True }, Api.approveEdit Approved model.sermon.id, None )

        Approved result ->
            case result of
                Ok sermon ->
                    ( model, Cmd.none, ApprovedSermon sermon )

                Err _ ->
                    ( { model | applying = False, confirmingApproval = False, error = Just "Could not approve the final audio. Please try again." }, Cmd.none, None )

        Back ->
            if busy model then
                ( model, Cmd.none, None )

            else if model.dirty then
                ( { model | backWarning = True }, Cmd.none, None )

            else
                ( model, Cmd.none, Close )

        ForceClose ->
            ( model, Cmd.none, Close )

        KeepEditing ->
            ( { model | backWarning = False }, Cmd.none, None )


finish model =
    let
        ready =
            resolve model
    in
    ( ready
    , Cmd.none
    , if List.isEmpty ready.regions then
        None

      else
        renderEffect ready
    )


finishDiscardDraft model =
    let
        ( ready, command, effect ) =
            finish model
    in
    case effect of
        Render render preview ->
            ( ready, command, Complete model.sermon.id render preview )

        _ ->
            ( ready, command, ClearDraft model.sermon.id )


changed model =
    let
        next =
            { model | dirty = True, finalReady = False, error = Nothing }
    in
    case renderEffect next of
        Render render preview ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) render preview )

        _ ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) Encode.null (previewValue next) )


changeBoundary boundary time model =
    changed { model | regions = Timeline.changeBoundary boundary time model.regions }


pipeline sermon model =
    if sermon.id /= model.sermon.id then
        ( model, None )

    else if sermon.editApproved then
        ( model, ApprovedSermon sermon )

    else if sermon.stage == "edit" && sermon.status == "done" then
        case sermon.appliedRegions of
            Just regions ->
                if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
                    -- Resolve the local draft before deciding whether this is a
                    -- new completion or a reconnect describing an older render.
                    ( { model | sermon = sermon, applying = False, finalReady = True }, None )

                else if model.dirty && model.regions /= regions then
                    -- A reconnect snapshot describes the last successful render,
                    -- not necessarily the user's newer local draft.
                    ( { model | sermon = sermon, applying = False }, None )

                else
                    let
                        next =
                            { model | sermon = sermon, regions = regions, applying = False, finalReady = True, dirty = False, error = Nothing }
                    in
                    case renderEffect next of
                        Render render preview ->
                            ( next, Complete sermon.id render preview )

                        _ ->
                            ( next, ClearDraft sermon.id )

            Nothing ->
                ( { model | sermon = sermon, applying = False, error = Just "The completed edit did not include its persisted regions." }, None )

    else if sermon.stage == "edit" && sermon.status == "failed" then
        ( { model | sermon = sermon, applying = False, error = sermon.error }, None )

    else
        ( { model | sermon = sermon, applying = sermon.stage == "edit" && (sermon.status == "pending" || sermon.status == "running") }, None )


valid duration regions =
    Timeline.valid duration regions


get index list =
    List.drop index list |> List.head


encodeRegions =
    Timeline.encodeRegions


encodeTimeline model =
    Encode.object [ ( "duration", Encode.float (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) ), ( "regions", encodeRegions model.regions ) ]


previewValue model =
    Encode.object
        [ ( "mode"
          , Encode.string
                (if model.finalReady then
                    "final"

                 else
                    "preview"
                )
          )
        , ( "regions", encodeRegions model.regions )
        ]


renderEffect model =
    case model.waveform of
        Just wave ->
            Render
                (Encode.object
                    [ ( "duration", Encode.float wave.duration )
                    , ( "samples", Encode.list Encode.float wave.samples )
                    , ( "regions", encodeRegions model.regions )
                    , ( "playhead", Encode.float model.playhead )
                    , ( "selected", Encode.int model.selected )
                    , ( "view_start", Encode.float model.viewStart )
                    , ( "view_end", Encode.float (model.viewStart + visibleSpan model) )
                    ]
                )
                (previewValue model)

        Nothing ->
            None


view model =
    div [ class "editor" ]
        [ div [ class "editor__header" ]
            [ timelineIconButton "Back to sermons" "mdi:arrow-left" Back (busy model)
            , div []
                [ h1 [ class "editor__title" ] [ text "Edit audio" ]
                , p [ class "editor__filename" ] [ text model.sermon.originalFilename ]
                ]
            ]
        , viewBody model
        ]


viewBody model =
    if loadFailed model then
        div [ class "editor__load-error" ]
            [ viewError model.error
            , button [ Ui.button, onClick ForceClose ] [ text "Back to Sermons" ]
            ]

    else if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
        p [ class "editor__loading" ] [ text "Loading waveform and finding sections…" ]

    else
        div []
            [ viewError model.error
            , div [ class "timeline-stage" ]
                [ canvas
                    [ id "timeline-canvas"
                    , class "timeline focusable"
                    , attribute "role" "img"
                    , attribute "aria-label" "Read-only audio waveform. Tap a section to select it for editing below."
                    , attribute "aria-readonly" "true"
                    , attribute "data-busy"
                        (if busy model then
                            "true"

                         else
                            "false"
                        )
                    , tabindex 0
                    ]
                    []
                ]
            , viewAudioPreview model
            , viewStatus model
            , viewRegionInspector model
            , div [ class "editor__actions" ]
                [ button [ Ui.primaryButton, onClick Apply, disabled (busy model || model.finalReady || not (validPlan model)) ]
                    [ text
                        (if model.applying then
                            "Rendering…"

                         else
                            "Apply Edits"
                        )
                    ]
                , if model.finalReady then
                    button [ Ui.dangerButton, onClick ConfirmApproval, disabled model.applying ] [ text "Approve Final Audio" ]

                  else
                    text ""
                , button [ Ui.button, onClick Back, disabled (busy model) ] [ text "Back to Sermons" ]
                ]
            , approval model
            , backWarning model
            ]


validPlan model =
    valid (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) model.regions && List.any .keep model.regions


viewAudioPreview model =
    div [ class "audio-preview" ]
        [ strong [ class "audio-preview__label" ]
            [ text
                (if model.finalReady then
                    "Rendered final audio"

                 else
                    "Edit preview"
                )
            ]
        , audio [ id "timeline-audio", class "editor__audio", controls True, src (audioSource model) ] []
        ]


audioSource model =
    if model.finalReady then
        "/api/sermons/" ++ model.sermon.id ++ "/audio/final?v=" ++ String.fromInt model.sermon.progress

    else
        proxyAudioSource model


proxyAudioSource model =
    "/api/sermons/"
        ++ model.sermon.id
        ++ "/audio/proxy?gate="
        ++ String.fromInt model.sermon.normalizationGateAdjustment
        ++ "&volume="
        ++ String.fromInt model.sermon.normalizationVolumeAdjustment


viewStatus model =
    if model.applying then
        p [ class "editor__status" ] [ text ("Rendering final audio… " ++ String.fromInt model.sermon.progress ++ "%") ]

    else if model.finalReady then
        p [ class "editor__status" ] [ strong [] [ text "Final audio is ready. Listen carefully, then approve or adjust sections and apply again." ] ]

    else
        text ""


viewError error =
    error |> Maybe.map (\e -> p [ Ui.errorText ] [ text e ]) |> Maybe.withDefault (text "")


approval model =
    if model.confirmingApproval then
        div [ Ui.confirmBox ] [ p [ Ui.confirmBoxQuestion ] [ strong [] [ text "This permanently deletes the raw recording. You won't be able to re-edit this sermon. Approve?" ] ], div [ Ui.confirmBoxButtons ] [ button [ Ui.dangerButton, onClick Approve, disabled model.applying ] [ text "Yes, Approve Permanently" ], button [ Ui.button, onClick CancelApproval ] [ text "Cancel" ] ] ]

    else
        text ""


backWarning model =
    if model.backWarning then
        div [ class "editor-warning" ] [ p [] [ strong [] [ text "You have unapplied changes. Your draft is saved in this browser." ] ], button [ Ui.button, onClick ForceClose ] [ text "Leave Editor" ], button [ Ui.button, onClick KeepEditing ] [ text "Keep Editing" ] ]

    else
        text ""


viewRegionInspector model =
    case get model.selected model.regions of
        Nothing ->
            text ""

        Just region ->
            div [ class "region-inspector" ]
                [ div [ class "region-inspector__nav" ]
                    [ timelineIconButton "Previous section" "mdi:chevron-left" SelectPrevious (model.selected <= 0)
                    , div [ class "region-inspector__identity" ]
                        [ strong [ class "region-inspector__title" ]
                            [ text (regionLabel region.regionType) ]
                        , span [ class "region-inspector__subtitle" ]
                            [ text ("Section " ++ String.fromInt (model.selected + 1) ++ "/" ++ String.fromInt (List.length model.regions)) ]
                        , span [ class "region-inspector__time" ]
                            [ text (Timeline.timestamp region.start ++ " – " ++ Timeline.timestamp region.end) ]
                        ]
                    , timelineIconButton "Next section" "mdi:chevron-right" SelectNext (model.selected >= List.length model.regions - 1)
                    ]
                , div [ class "region-inspector__preview" ]
                    [ strong [ class "region-inspector__preview-label" ] [ text "Section audio" ]
                    , audio
                        [ id "section-audio"
                        , class "region-inspector__audio"
                        , controls True
                        , attribute "preload" "metadata"
                        , attribute "data-start" (String.fromFloat region.start)
                        , attribute "data-end" (String.fromFloat region.end)
                        , src (proxyAudioSource model)
                        ]
                        []
                    ]
                , div [ class "region-inspector__decision" ]
                    [ decisionButton "Keep" "mdi:check" (SetKeep model.selected True) region.keep (busy model)
                    , decisionButton "Delete" "mdi:content-cut" (SetKeep model.selected False) (not region.keep) (busy model)
                    ]
                , div [ class "precision-editor" ]
                    [ div [ class "precision-editor__header" ]
                        [ strong [] [ text "Boundary adjustment" ]
                        , div [ class "precision-editor__steps" ]
                            [ stepButton model 0.01
                            , stepButton model 0.1
                            , stepButton model 1
                            ]
                        ]
                    , if model.selected > 0 then
                        boundaryRow model "Start" model.selected region.start

                      else
                        text ""
                    , if model.selected < List.length model.regions - 1 then
                        boundaryRow model "End" (model.selected + 1) region.end

                      else
                        text ""
                    ]
                ]


regionLabel regionType =
    case regionType of
        "speaking" ->
            "Speech"

        "singing" ->
            "Singing"

        _ ->
            "Silence"


decisionButton label iconName message selected isBusy =
    button
        [ class
            (if selected then
                "decision-button decision-button--selected"

             else
                "decision-button"
            )
        , onClick message
        , disabled (isBusy || selected)
        , attribute "aria-pressed"
            (if selected then
                "true"

             else
                "false"
            )
        ]
        [ icon iconName
        , text label
        ]


stepButton model step =
    button
        [ class
            (if model.nudgeStep == step then
                "step-button step-button--selected"

             else
                "step-button"
            )
        , onClick (SetNudgeStep step)
        , attribute "aria-pressed"
            (if model.nudgeStep == step then
                "true"

             else
                "false"
            )
        ]
        [ text
            (if step == 1 then
                "1s"

             else
                String.fromFloat step ++ "s"
            )
        ]


boundaryRow model label boundary time =
    div [ class "precision-editor__row" ]
        [ span [ class "precision-editor__label" ] [ text label ]
        , timelineIconButton ("Move " ++ String.toLower label ++ " earlier") "mdi:minus" (Nudge boundary -model.nudgeStep) (busy model)
        , strong [ class "precision-editor__time" ] [ text (Timeline.timestamp time) ]
        , timelineIconButton ("Move " ++ String.toLower label ++ " later") "mdi:plus" (Nudge boundary model.nudgeStep) (busy model)
        ]


busy model =
    model.applying || (model.sermon.stage == "edit" && (model.sermon.status == "pending" || model.sermon.status == "running"))


loadFailed model =
    model.error /= Nothing && (model.waveform == Nothing || model.analysis == Nothing)


timelineIconButton label iconName message isDisabled =
    button
        [ Ui.iconButton
        , onClick message
        , disabled isDisabled
        , attribute "aria-label" label
        , title label
        ]
        [ icon iconName ]


icon iconName =
    node "iconify-icon"
        [ class "button__icon"
        , attribute "icon" iconName
        , attribute "width" "24"
        , attribute "height" "24"
        , attribute "aria-hidden" "true"
        ]
        []


redraw model =
    ( model, Cmd.none, renderEffect model )


selectRegion requested model =
    let
        index =
            max 0 (min (List.length model.regions - 1) requested)

        selected =
            get index model.regions

        span =
            visibleSpan model

        start =
            selected
                |> Maybe.andThen
                    (\region ->
                        if region.start < model.viewStart || region.end > model.viewStart + span then
                            Just (clampViewStart model ((region.start + region.end - span) / 2))

                        else
                            Nothing
                    )
                |> Maybe.withDefault model.viewStart
    in
    { model | selected = index, viewStart = start }


setZoom requested model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0

        oldSpan =
            visibleSpan model

        center =
            model.viewStart + oldSpan / 2

        zoom =
            max 1 (min 512 requested)

        newSpan =
            if zoom <= 0 then
                duration

            else
                duration / zoom

        next =
            { model | zoom = zoom }
    in
    { next | viewStart = clampViewStart next (center - newSpan / 2) }


visibleSpan model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    if model.zoom <= 0 then
        duration

    else
        duration / model.zoom


clampViewStart model requested =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    max 0 (min (max 0 (duration - visibleSpan model)) requested)
