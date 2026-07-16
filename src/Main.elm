module Main exposing (main)

import Browser
import File exposing (File)
import Html exposing (Html, button, div, h1, h2, input, label, p, span, strong, text)
import Html.Attributes exposing (accept, class, disabled, id, style, type_)
import Html.Events exposing (on, onClick)
import Http
import Iso8601
import Json.Decode as Decode exposing (Decoder)
import Task
import Time
import Ui


main : Program () Model Msg
main =
    Browser.element
        { init = init
        , update = update
        , view = view
        , subscriptions = subscriptions
        }



-- MODEL


type alias Sermon =
    { id : String
    , originalFilename : String
    , uploadedAt : String
    , uploadedBy : Maybe String
    , stage : String
    , status : String
    }


type SermonList
    = Loading
    | Loaded (List Sermon)
    | LoadFailed


type UploadState
    = Idle
    | Uploading Float
    | UploadFailed String


type alias Model =
    { sermons : SermonList
    , upload : UploadState
    , confirmingDelete : Maybe Sermon
    , zone : Time.Zone
    }


init : () -> ( Model, Cmd Msg )
init _ =
    ( { sermons = Loading
      , upload = Idle
      , confirmingDelete = Nothing
      , zone = Time.utc
      }
    , Cmd.batch [ fetchSermons, Task.perform GotZone Time.here ]
    )



-- UPDATE


type Msg
    = GotZone Time.Zone
    | GotSermons (Result Http.Error (List Sermon))
    | FilePicked File
    | UploadProgress Http.Progress
    | UploadFinished (Result Http.Error ())
    | AskDelete Sermon
    | CancelDelete
    | ConfirmDelete Sermon
    | DeleteFinished (Result Http.Error ())


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        GotZone zone ->
            ( { model | zone = zone }, Cmd.none )

        GotSermons (Ok sermons) ->
            ( { model | sermons = Loaded sermons }, Cmd.none )

        GotSermons (Err _) ->
            ( { model | sermons = LoadFailed }, Cmd.none )

        FilePicked file ->
            ( { model | upload = Uploading 0 }
            , uploadFile file
            )

        UploadProgress progress ->
            case progress of
                Http.Sending sent ->
                    ( { model | upload = Uploading (Http.fractionSent sent) }
                    , Cmd.none
                    )

                Http.Receiving _ ->
                    ( model, Cmd.none )

        UploadFinished (Ok ()) ->
            ( { model | upload = Idle }, fetchSermons )

        UploadFinished (Err err) ->
            ( { model | upload = UploadFailed (uploadErrorMessage err) }
            , Cmd.none
            )

        AskDelete sermon ->
            ( { model | confirmingDelete = Just sermon }, Cmd.none )

        CancelDelete ->
            ( { model | confirmingDelete = Nothing }, Cmd.none )

        ConfirmDelete sermon ->
            ( { model | confirmingDelete = Nothing }
            , deleteSermon sermon.id
            )

        DeleteFinished _ ->
            ( model, fetchSermons )


subscriptions : Model -> Sub Msg
subscriptions model =
    case model.upload of
        Uploading _ ->
            Http.track uploadTracker UploadProgress

        _ ->
            Sub.none



-- HTTP


uploadTracker : String
uploadTracker =
    "sermon-upload"


fetchSermons : Cmd Msg
fetchSermons =
    Http.get
        { url = "/api/sermons"
        , expect = Http.expectJson GotSermons (Decode.list sermonDecoder)
        }


uploadFile : File -> Cmd Msg
uploadFile file =
    Http.request
        { method = "POST"
        , headers = []
        , url = "/api/sermons"
        , body = Http.multipartBody [ Http.filePart "file" file ]
        , expect = Http.expectWhatever UploadFinished
        , timeout = Nothing
        , tracker = Just uploadTracker
        }


deleteSermon : String -> Cmd Msg
deleteSermon id =
    Http.request
        { method = "DELETE"
        , headers = []
        , url = "/api/sermons/" ++ id
        , body = Http.emptyBody
        , expect = Http.expectWhatever DeleteFinished
        , timeout = Nothing
        , tracker = Nothing
        }


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map6 Sermon
        (Decode.field "id" Decode.string)
        (Decode.field "original_filename" Decode.string)
        (Decode.field "uploaded_at" Decode.string)
        (Decode.field "uploaded_by" (Decode.nullable Decode.string))
        (Decode.field "stage" Decode.string)
        (Decode.field "status" Decode.string)


uploadErrorMessage : Http.Error -> String
uploadErrorMessage err =
    case err of
        Http.BadStatus 413 ->
            "That file is too large. The limit is 2 GB."

        Http.NetworkError ->
            "The connection was interrupted. Please try again."

        Http.Timeout ->
            "The upload took too long. Please try again."

        _ ->
            "Something went wrong with the upload. Please try again."



-- VIEW


view : Model -> Html Msg
view model =
    div [ class "page" ]
        [ div [ class "masthead" ]
            [ h1 [] [ text "Sermon Scribe" ] ]
        , viewUpload model.upload
        , h2 [] [ text "Sermons" ]
        , viewSermons model
        ]


viewUpload : UploadState -> Html Msg
viewUpload upload =
    case upload of
        Uploading fraction ->
            div [ class "upload-box" ]
                [ p [ class "upload-box__status" ]
                    [ text ("Uploading\u{2026} " ++ percent fraction) ]
                , viewProgressBar fraction
                , p [ Ui.hint ]
                    [ text "Please keep this page open until the upload finishes." ]
                ]

        _ ->
            div [ class "upload-box" ]
                (List.concat
                    [ [ label [ Ui.primaryButton, Html.Attributes.for "file-input" ]
                            [ text "Upload a Sermon Recording" ]
                      , input
                            [ type_ "file"
                            , id "file-input"
                            , accept "audio/*"
                            , on "change" fileChangeDecoder
                            , style "display" "none"
                            ]
                            []
                      , p [ Ui.hint ]
                            [ text "Choose the audio file from your computer. The upload starts right away." ]
                      ]
                    , case upload of
                        UploadFailed message ->
                            [ p [ Ui.errorText ] [ text message ] ]

                        _ ->
                            []
                    ]
                )


fileChangeDecoder : Decoder Msg
fileChangeDecoder =
    Decode.at [ "target", "files" ] (Decode.index 0 File.decoder)
        |> Decode.map FilePicked


viewProgressBar : Float -> Html Msg
viewProgressBar fraction =
    div [ Ui.progress ]
        [ div
            [ Ui.progressFill
            , style "width" (percent fraction)
            ]
            []
        ]


percent : Float -> String
percent fraction =
    String.fromInt (round (fraction * 100)) ++ "%"


viewSermons : Model -> Html Msg
viewSermons model =
    case model.sermons of
        Loading ->
            p [ Ui.hint ] [ text "Loading\u{2026}" ]

        LoadFailed ->
            p [ Ui.errorText ]
                [ text "Could not load the sermon list. Please reload the page." ]

        Loaded [] ->
            div [ Ui.emptyState ]
                [ text "No sermons yet. Upload one to get started." ]

        Loaded sermons ->
            div [] (List.map (viewSermon model) sermons)


viewSermon : Model -> Sermon -> Html Msg
viewSermon model sermon =
    div [ Ui.card ]
        [ div [ Ui.cardInfo ]
            [ p [ Ui.cardName ] [ text sermon.originalFilename ]
            , p [ Ui.cardMeta ]
                [ span [ badgeAttribute sermon ] [ text (describeStage sermon) ]
                , text (formatDate model.zone sermon.uploadedAt)
                ]
            ]
        , viewSermonActions model sermon
        ]


viewSermonActions : Model -> Sermon -> Html Msg
viewSermonActions model sermon =
    case model.confirmingDelete of
        Just pending ->
            if pending.id == sermon.id then
                div [ Ui.confirmBox ]
                    [ p [ Ui.confirmBoxQuestion ]
                        [ strong [] [ text "Delete this sermon and its audio files?" ] ]
                    , div [ Ui.confirmBoxButtons ]
                        [ button
                            [ Ui.dangerButton, onClick (ConfirmDelete sermon) ]
                            [ text "Yes, Delete" ]
                        , button
                            [ Ui.button, onClick CancelDelete ]
                            [ text "Cancel" ]
                        ]
                    ]

            else
                viewDeleteButton sermon True

        Nothing ->
            viewDeleteButton sermon False


viewDeleteButton : Sermon -> Bool -> Html Msg
viewDeleteButton sermon isDisabled =
    div [ class "sermon-actions" ]
        [ button
            [ Ui.button, onClick (AskDelete sermon), disabled isDisabled ]
            [ text "Delete" ]
        ]


{-| Render a stage/status pair in plain language. Later specs add more
stages; unknown combinations fall back to a generic rendering.
-}
badgeAttribute : Sermon -> Html.Attribute Msg
badgeAttribute sermon =
    if sermon.status == "failed" || sermon.status == "error" then
        Ui.badgeFailed

    else
        Ui.badge


describeStage : Sermon -> String
describeStage sermon =
    case ( sermon.stage, sermon.status ) of
        ( "upload", "done" ) ->
            "Uploaded"

        ( stage, "done" ) ->
            capitalize stage ++ " finished"

        ( stage, "running" ) ->
            capitalize stage ++ " in progress"

        ( stage, "error" ) ->
            capitalize stage ++ " failed"

        ( stage, status ) ->
            capitalize stage ++ ": " ++ status


capitalize : String -> String
capitalize s =
    case String.uncons s of
        Just ( first, rest ) ->
            String.cons (Char.toUpper first) rest

        Nothing ->
            s



-- DATES


formatDate : Time.Zone -> String -> String
formatDate zone iso =
    case Iso8601.toTime iso of
        Ok posix ->
            monthName (Time.toMonth zone posix)
                ++ " "
                ++ String.fromInt (Time.toDay zone posix)
                ++ ", "
                ++ String.fromInt (Time.toYear zone posix)

        Err _ ->
            iso


monthName : Time.Month -> String
monthName month =
    case month of
        Time.Jan ->
            "January"

        Time.Feb ->
            "February"

        Time.Mar ->
            "March"

        Time.Apr ->
            "April"

        Time.May ->
            "May"

        Time.Jun ->
            "June"

        Time.Jul ->
            "July"

        Time.Aug ->
            "August"

        Time.Sep ->
            "September"

        Time.Oct ->
            "October"

        Time.Nov ->
            "November"

        Time.Dec ->
            "December"
